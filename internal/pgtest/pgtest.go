// Package pgtest starts the PostgreSQL server tests and schema-dump apply the migrations to
package pgtest

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bcmk/siren/v6/lib/cmdlib"
)

// Image is the Apache-2 TimescaleDB build, at the version production runs
const Image = "timescale/timescaledb:2.29.1-pg18-oss"

// Run starts a server whose first database is dbName.
// TimescaleDB's job scheduler is off:
// it sits on every database holding the extension and blocks create database ... template.
// NO_TS_TUNE keeps the image from sizing the server to the developer's VM:
// a small one gets max_connections = 25, fewer than the bot tests open.
func Run(ctx context.Context, dbName string) (*postgres.PostgresContainer, error) {
	return postgres.Run(
		ctx,
		Image,
		postgres.WithDatabase(dbName),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
		testcontainers.WithCmdArgs("-c", "timescaledb.max_background_workers=0"),
		testcontainers.WithEnv(map[string]string{"NO_TS_TUNE": "true"}),
	)
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
	// admin issues create database against the maintenance database.
	// pgx connections are not goroutine safe, so adminMu guards it,
	// which also keeps concurrent clones off the same template.
	admin   *pgx.Conn
	adminMu sync.Mutex
	counter atomic.Int64
}

// NewClones returns Clones whose template migrate migrates.
// The container starts on the first Clone, so a run with no database test pays nothing for it.
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

// Clone creates a database from the migrated template and returns its connection string
func (c *Clones) Clone() (string, error) {
	if err := c.ready(); err != nil {
		return "", err
	}
	// Generated, so it needs no quoting: identifiers cannot be query args
	name := fmt.Sprintf("test_%d", c.counter.Add(1))
	c.adminMu.Lock()
	defer c.adminMu.Unlock()
	// No drop database: the container's teardown takes every clone with it
	_, err := c.admin.Exec(context.Background(), fmt.Sprintf("create database %s template %s", name, templateName))
	return c.connStrFor(name), err
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
