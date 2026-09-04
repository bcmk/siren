package db

import (
	"context"
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type migration struct {
	order         int
	name          string
	sql           string
	noTransaction bool
	// prebuild migrations are ordinary ones that migrator --prebuild applies early.
	prebuild bool
}

func loadMigrations() ([]migration, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("reading migrations directory: %w", err)
	}

	var migrations []migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		order, name, noTransaction, prebuild, err := parseMigrationFilename(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("parsing migration filename %s: %w", entry.Name(), err)
		}

		content, err := migrationsFS.ReadFile(filepath.Join("migrations", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading migration %s: %w", entry.Name(), err)
		}

		migrations = append(migrations, migration{
			order:         order,
			name:          name,
			sql:           string(content),
			noTransaction: noTransaction,
			prebuild:      prebuild,
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].order < migrations[j].order
	})

	return migrations, nil
}

// parseMigrationFilename reads NNNN_[prebuild_][no_transaction_]name.sql.
// The recorded name holds neither prefix,
// so adding or dropping one later leaves the migration's identity,
// and what schema_migrations already has, alone.
func parseMigrationFilename(filename string) (int, string, bool, bool, error) {
	base := strings.TrimSuffix(filename, ".sql")
	parts := strings.SplitN(base, "_", 2)
	if len(parts) != 2 {
		return 0, "", false, false, fmt.Errorf("invalid migration filename format: %s", filename)
	}

	order, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", false, false, fmt.Errorf("invalid order number in %s: %w", filename, err)
	}

	name := parts[1]
	prebuild := strings.HasPrefix(name, "prebuild_")
	name = strings.TrimPrefix(name, "prebuild_")
	noTransaction := strings.HasPrefix(name, "no_transaction_")
	name = strings.TrimPrefix(name, "no_transaction_")
	if strings.HasPrefix(name, "prebuild_") {
		return 0, "", false, false,
			fmt.Errorf("prebuild must come before no_transaction in %s", filename)
	}

	return order, name, noTransaction, prebuild, nil
}

func (d *Database) isMigrationApplied(name string) bool {
	var exists bool
	err := d.db.QueryRow(
		context.Background(),
		`
			select exists(select 1 from schema_migrations where name = $1)
			/*name='is_migration_applied'*/
		`,
		name,
	).Scan(&exists)
	if err == pgx.ErrNoRows || err != nil {
		return false
	}
	return exists
}

// migrationLock is an arbitrary key, shared by everything that applies migrations.
// A prebuild records itself only once it finishes,
// so for the hours it runs it still reads as pending,
// and a second runner would convert the same rows again.
const migrationLock = 0x5152454E

// lockMigrations blocks until this is the only migration run, and says so while it waits.
func (d *Database) lockMigrations() {
	acquired := d.MustBool(`
		select pg_try_advisory_lock($1)
		/*name='lock_migrations_try'*/`,
		migrationLock)
	if !acquired {
		linf("another migration run holds the lock, waiting for it")
		d.MustExec(`
			select pg_advisory_lock($1)
			/*name='lock_migrations_wait'*/`,
			migrationLock)
	}
}

// unlockMigrations is best effort: the lock dies with the session anyway,
// and failing here while a migration error unwinds would replace it with something less useful.
func (d *Database) unlockMigrations() {
	_, _ = d.db.Exec(context.Background(), `
		select pg_advisory_unlock($1)
		/*name='unlock_migrations'*/`,
		migrationLock)
}

// ApplyMigrations applies all migrations to the database
func (d *Database) ApplyMigrations() {
	d.lockMigrations()
	defer d.unlockMigrations()

	migrations, err := loadMigrations()
	checkErr(err)

	for _, m := range migrations {
		// Check DB each time — migration 0000 populates schema_migrations
		// with all existing migration names, so they'll be skipped.
		if d.isMigrationApplied(m.name) {
			continue
		}
		linf("applying migration %s...", m.name)
		d.applyMigration(m)
	}
	linf("no more migrations")
}

// ApplyNextPrebuildMigrations applies the earliest prebuild migration not yet applied
// and those consecutive with it, reporting whether it found any.
// A prebuild often needs a transactional step and a no_transaction one, so it runs as a group.
func (d *Database) ApplyNextPrebuildMigrations() bool {
	d.lockMigrations()
	defer d.unlockMigrations()

	migrations, err := loadMigrations()
	checkErr(err)

	// A prebuild assumes the schema the migrations before it leave behind,
	// so it runs only while it is the first thing pending.
	start := -1
	for i, m := range migrations {
		if d.isMigrationApplied(m.name) {
			continue
		}
		if m.prebuild {
			start = i
		} else if blocked := d.pendingPrebuild(migrations[i:]); blocked != "" {
			checkErr(fmt.Errorf(
				"migration %s is pending, let the bot apply it before prebuilding %s", m.name, blocked))
		}
		break
	}
	if start < 0 {
		linf("no prebuild migrations pending")
		return false
	}

	for _, m := range migrations[start:] {
		if !m.prebuild {
			break
		}
		if d.isMigrationApplied(m.name) {
			continue
		}
		linf("applying prebuild migration %s...", m.name)
		d.applyMigration(m)
	}

	linf("prebuild migrations applied")
	return true
}

// pendingPrebuild names the first prebuild migration still to apply, if any.
func (d *Database) pendingPrebuild(migrations []migration) string {
	for _, m := range migrations {
		if m.prebuild && !d.isMigrationApplied(m.name) {
			return m.name
		}
	}
	return ""
}

// applyMigration runs a migration and records it.
// The transactional case commits both together,
// so an interrupted run leaves the migration to be applied rather than half applied and unrecorded,
// which replay would fail on.
// A no_transaction migration cannot have that, and must therefore be one that a replay can survive.
func (d *Database) applyMigration(m migration) {
	const record = `
		insert into schema_migrations (name, applied_at) values ($1, $2) on conflict do nothing
		/*name='record_migration'*/`

	if m.noTransaction {
		d.MustExecScript(m.sql)
		d.MustExec(record, m.name, time.Now().Unix())
		return
	}

	ctx := context.Background()
	tx, err := d.Begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, m.sql)
	checkErr(err)
	_, err = tx.Exec(ctx, record, m.name, time.Now().Unix())
	checkErr(err)
	checkErr(tx.Commit(ctx))
}
