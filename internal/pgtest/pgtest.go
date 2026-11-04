// Package pgtest starts the PostgreSQL server tests and schema-dump apply the migrations to
package pgtest

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bcmk/siren/v6/lib/cmdlib"
)

// Image is the Apache-2 TimescaleDB build, at the version production runs
const Image = "timescale/timescaledb:2.22.1-pg17-oss"

// Run starts a server whose first database is dbName.
// TimescaleDB's job scheduler is off:
// it sits on every database holding the extension and blocks create database ... template.
// pg_stat_statements is loaded, as in production, for the tests that read it.
// It tracks only the sessions that ask, as a full table would evict their entries.
// NO_TS_TUNE keeps the image from sizing the server to the developer's VM:
// a small one gets max_connections = 25, which parallel tests can run out of.
// The data lives on tmpfs, where clones run faster than on the container's disk.
func Run(ctx context.Context, dbName string) (*postgres.PostgresContainer, error) {
	return postgres.Run(
		ctx,
		Image,
		postgres.WithDatabase(dbName),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
		testcontainers.WithCmdArgs(
			"-c", "timescaledb.max_background_workers=0",
			"-c", "shared_preload_libraries=timescaledb,pg_stat_statements",
			"-c", "pg_stat_statements.track=none"),
		testcontainers.WithEnv(map[string]string{"NO_TS_TUNE": "true"}),
		testcontainers.WithTmpfs(map[string]string{"/var/lib/postgresql/data": "rw"}),
	)
}

// PgDump runs pg_dump on dbName and returns its output, without the random \restrict lines.
func PgDump(ctx context.Context, container *postgres.PostgresContainer, dbName string, args ...string) (string, error) {
	// stderr holds warnings a dump must not carry, so it shows only on failure
	cmd := `e=$(mktemp); trap 'rm -f "$e"' EXIT; pg_dump -U test -d ` + dbName + " " + strings.Join(args, " ") +
		` 2>"$e" || { c=$?; cat "$e"; exit $c; }`
	code, out, err := container.Exec(ctx, []string{"sh", "-c", cmd}, tcexec.Multiplexed())
	if err != nil {
		return "", err
	}
	dump, err := io.ReadAll(out)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("pg_dump of %s exited with %d: %s", dbName, code, dump)
	}
	lines := strings.SplitAfter(string(dump), "\n")
	lines = slices.DeleteFunc(lines, func(line string) bool {
		return strings.HasPrefix(line, `\restrict `) || strings.HasPrefix(line, `\unrestrict `)
	})
	return strings.Join(lines, ""), nil
}

// templateName is the database migrations are applied to once per run.
// Each test clones it,
// so a test pays for a file copy rather than a container start plus every migration.
const templateName = "test"

// Clones gives each test a database of its own, cloned from a migrated template,
// so tests stay isolated and can run in parallel
type Clones struct {
	migrate func(connStr string) error
	// ready runs start once, and re-panics in every caller if start panicked
	ready     func() error
	container *postgres.PostgresContainer
	// baseConnStr points at the template; connStrFor rewrites its path to reach the other databases
	baseConnStr string
	// admin issues create and drop database against the maintenance database.
	// pgx connections are not goroutine safe, so adminMu guards it,
	// which also keeps concurrent clones off the same template.
	admin   *pgx.Conn
	adminMu sync.Mutex
	counter atomic.Int64
}

// NewClones returns Clones whose template migrate migrates.
// The container starts on the first database, so a run with no database test pays nothing for it.
func NewClones(migrate func(connStr string) error) *Clones {
	c := &Clones{migrate: migrate}
	c.ready = sync.OnceValue(c.start)
	return c
}

func (c *Clones) start() error {
	ctx := context.Background()
	var err error
	if c.container, err = Run(ctx, templateName); err != nil {
		return err
	}
	if c.baseConnStr, err = c.container.ConnectionString(ctx, "sslmode=disable"); err != nil {
		return err
	}
	// migrate disconnects: create database rejects a template that still has clients
	if err := c.migrate(c.baseConnStr); err != nil {
		return err
	}
	c.admin, err = pgx.Connect(ctx, c.connStrFor("postgres"))
	return err
}

// Clone creates a database from the migrated template; drop removes it to free its tmpfs space.
func (c *Clones) Clone() (connStr string, drop func() error, err error) {
	if err = c.ready(); err != nil {
		return "", nil, err
	}
	// Generated, so it needs no quoting: identifiers cannot be query args
	name := fmt.Sprintf("test_%d", c.counter.Add(1))
	drop = func() error {
		return c.adminExec(fmt.Sprintf("drop database %s", name))
	}
	// file_copy clones faster than the default wal_log under the tests' parallel load
	err = c.adminExec(fmt.Sprintf("create database %s template %s strategy = file_copy", name, templateName))
	return c.connStrFor(name), drop, err
}

// EmptyDatabase creates a database with nothing in it; drop removes it.
func (c *Clones) EmptyDatabase() (connStr string, drop func() error, err error) {
	if err = c.ready(); err != nil {
		return "", nil, err
	}
	name := fmt.Sprintf("test_%d", c.counter.Add(1))
	drop = func() error {
		return c.adminExec(fmt.Sprintf("drop database %s", name))
	}
	// Not template1, which holds the extensions the image installed
	err = c.adminExec(fmt.Sprintf("create database %s template template0", name))
	return c.connStrFor(name), drop, err
}

// PgDump runs pg_dump on the database connStr points at
func (c *Clones) PgDump(connStr string, args ...string) (string, error) {
	u, err := url.Parse(connStr)
	if err != nil {
		return "", err
	}
	return PgDump(context.Background(), c.container, strings.TrimPrefix(u.Path, "/"), args...)
}

func (c *Clones) adminExec(sql string) error {
	c.adminMu.Lock()
	defer c.adminMu.Unlock()
	_, err := c.admin.Exec(context.Background(), sql)
	return err
}

// connStrFor returns the connection string of another database on the container
func (c *Clones) connStrFor(dbName string) string {
	u, err := url.Parse(c.baseConnStr)
	cmdlib.CheckErr(err)
	u.Path = "/" + dbName
	return u.String()
}

// Terminate stops the container, if it started; call it once every test has returned
func (c *Clones) Terminate() {
	_ = testcontainers.TerminateContainer(c.container)
}
