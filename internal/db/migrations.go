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

		order, name, noTransaction, err := parseMigrationFilename(entry.Name())
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
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].order < migrations[j].order
	})

	return migrations, nil
}

func parseMigrationFilename(filename string) (int, string, bool, error) {
	base := strings.TrimSuffix(filename, ".sql")
	parts := strings.SplitN(base, "_", 2)
	if len(parts) != 2 {
		return 0, "", false, fmt.Errorf("invalid migration filename format: %s", filename)
	}

	order, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", false, fmt.Errorf("invalid order number in %s: %w", filename, err)
	}

	name := parts[1]
	noTransaction := strings.HasPrefix(name, "no_transaction_")
	if noTransaction {
		name = strings.TrimPrefix(name, "no_transaction_")
	}

	return order, name, noTransaction, nil
}

func (d *Database) isMigrationApplied(name string) bool {
	var exists bool
	err := d.db.QueryRow(
		context.Background(),
		"select exists(select 1 from schema_migrations where name = $1)",
		name,
	).Scan(&exists)
	if err == pgx.ErrNoRows || err != nil {
		return false
	}
	return exists
}

// ApplyMigrations applies all migrations to the database
func (d *Database) ApplyMigrations() {
	migrations, err := loadMigrations()
	checkErr(err)

	for _, m := range migrations {
		// Check DB each time — migration 0000 populates schema_migrations
		// with all existing migration names, so they'll be skipped.
		if d.isMigrationApplied(m.name) {
			continue
		}
		linf("applying migration %s...", m.name)
		if m.noTransaction {
			d.MustExecScript(m.sql)
		} else {
			d.MustExec(m.sql)
		}
		d.MustExec(
			"insert into schema_migrations (name, applied_at) values ($1, $2) on conflict do nothing",
			m.name,
			time.Now().Unix(),
		)
	}
	linf("no more migrations")
}
