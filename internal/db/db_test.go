package db

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bcmk/siren/v3/lib/cmdlib"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// templateDBName is the database migrations are applied to once per run.
// Each test clones it, so a test pays for a file copy
// rather than a container start plus 60-odd migrations.
const templateDBName = "test"

var (
	// pgOnce starts the container on first use, so a run with no database test
	// — go test -run TestGIDCheckSuspend — pays nothing for it.
	pgOnce      sync.Once
	pgContainer *postgres.PostgresContainer

	// baseConnStr points at templateDBName on the shared container.
	// connStrFor rewrites its path to reach the other databases.
	baseConnStr string

	// adminConn issues create database against the maintenance database.
	// pgx connections are not goroutine safe, so adminMu guards it,
	// which also keeps concurrent clones off the same template.
	adminConn *pgx.Conn
	adminMu   sync.Mutex

	dbCounter atomic.Int64
)

func TestMain(m *testing.M) {
	cmdlib.Verbosity = cmdlib.SilentVerbosity
	code := m.Run()
	// Reached only after every test has returned, so the once is settled.
	if pgContainer != nil {
		ctx := context.Background()
		_ = adminConn.Close(ctx)
		_ = pgContainer.Terminate(ctx)
	}
	os.Exit(code)
}

// startPostgres boots the shared container and migrates the template.
func startPostgres() {
	ctx := context.Background()

	var err error
	pgContainer, err = postgres.Run(
		ctx,
		"postgres:18",
		postgres.WithDatabase(templateDBName),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
	)
	checkErr(err)

	baseConnStr, err = pgContainer.ConnectionString(ctx, "sslmode=disable")
	checkErr(err)

	// Migrate the template, then disconnect:
	// create database rejects a template that still has clients.
	d := NewDatabase(baseConnStr, false, 5)
	d.ApplyMigrations()
	checkErr(d.Close())

	adminConn, err = pgx.Connect(ctx, connStrFor("postgres"))
	checkErr(err)
}

// connStrFor returns baseConnStr pointed at another database on the container.
func connStrFor(dbName string) string {
	u, err := url.Parse(baseConnStr)
	checkErr(err)
	u.Path = "/" + dbName
	return u.String()
}

type testDB struct {
	*Database
	terminate func()
}

// newTestDB clones the migrated template into a database of its own,
// so tests stay isolated and can run in parallel.
func newTestDB(t *testing.T) *testDB {
	t.Helper()
	pgOnce.Do(startPostgres)

	ctx := context.Background()
	// Generated, so it needs no quoting: identifiers cannot be query args.
	name := fmt.Sprintf("test_%d", dbCounter.Add(1))

	adminMu.Lock()
	_, err := adminConn.Exec(ctx, fmt.Sprintf("create database %s template %s", name, templateDBName))
	adminMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	d := NewDatabase(connStrFor(name), false, 5)
	// No drop database: the container's teardown takes every clone with it.
	return &testDB{
		Database:  &d,
		terminate: func() { _ = d.Close() },
	}
}
