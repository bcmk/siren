package db

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// templateName is the database the schema goes into once per process.
// Each test clones it, so a test pays for a file copy rather than for building the schema.
const templateName = "test"

// startServer starts a server whose first database is the template.
// TimescaleDB's job scheduler is off:
// it sits on every database holding the extension and blocks create database ... template.
// Autovacuum is off: it changes the stats and pages that the vacuum tests read.
// pg_stat_statements is loaded, as in production, for the tests that read it.
// It tracks only the sessions that ask, as a full table would evict their entries.
// NO_TS_TUNE keeps the image from sizing the server to the developer's VM:
// a small one gets max_connections = 25, fewer than the tests open.
// The data lives on tmpfs, where clones run faster than on the container's disk.
func startServer(ctx context.Context) (*postgres.PostgresContainer, error) {
	return postgres.Run(
		ctx,
		TimescaleImage,
		postgres.WithDatabase(templateName),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
		testcontainers.WithCmdArgs(
			"-c", "timescaledb.max_background_workers=0",
			"-c", "autovacuum=off",
			"-c", "shared_preload_libraries=timescaledb,pg_stat_statements",
			"-c", "pg_stat_statements.track=none"),
		testcontainers.WithEnv(map[string]string{"NO_TS_TUNE": "true"}),
		testcontainers.WithTmpfs(map[string]string{"/var/lib/postgresql/data": "rw"}))
}

// testServer gives each test a database of its own, cloned from a migrated template,
// so tests stay isolated and can run in parallel
type testServer struct {
	// ready runs start once, and re-panics in every caller if start panicked
	ready     func() error
	container *postgres.PostgresContainer
	// baseConnStr points at the template; connStrFor rewrites its path to reach the other databases
	baseConnStr string
	// admin issues create and drop database against the maintenance database.
	// A pool, as a batch of clones finishes sooner in parallel.
	admin   *pgxpool.Pool
	counter atomic.Int64
}

// newTestServer returns a server whose container starts on the first database,
// so a run with no database test pays nothing for it
func newTestServer() *testServer {
	s := &testServer{}
	s.ready = sync.OnceValue(s.start)
	return s
}

func (s *testServer) start() error {
	ctx := context.Background()
	var err error
	if s.container, err = startServer(ctx); err != nil {
		return err
	}
	if s.baseConnStr, err = s.container.ConnectionString(ctx, "sslmode=disable"); err != nil {
		return err
	}
	template := NewDatabase(s.baseConnStr, false, 5)
	template.ApplyMigrations()
	// create database rejects a template that still has clients
	if err := template.Close(); err != nil {
		return err
	}
	s.admin, err = pgxpool.New(ctx, s.connStrFor("postgres"))
	return err
}

// create creates a database with these options; drop removes it to free its tmpfs space.
func (s *testServer) create(options string) (connStr string, drop func() error, err error) {
	if err = s.ready(); err != nil {
		return "", nil, err
	}
	// Generated, so it needs no quoting: identifiers cannot be query args
	name := fmt.Sprintf("test_%d", s.counter.Add(1))
	drop = func() error {
		return s.adminExec("drop database " + name)
	}
	err = s.adminExec("create database " + name + " " + options)
	return s.connStrFor(name), drop, err
}

// clone creates a database from the migrated template
func (s *testServer) clone() (connStr string, drop func() error, err error) {
	// file_copy clones faster than the default wal_log under the tests' parallel load
	return s.create("template " + templateName + " strategy = file_copy")
}

// createEmptyDatabase creates a database with nothing in it
func (s *testServer) createEmptyDatabase() (connStr string, drop func() error, err error) {
	// Not template1, which holds the extensions the image installed
	return s.create("template template0")
}

// pgDumpSchema returns the schema pg_dump gives for a database.
// The fixed restrict key keeps the random one out of the comparison.
func (s *testServer) pgDumpSchema(dbName string) (string, error) {
	// stderr holds warnings a dump must not carry, so it shows only on failure
	cmd := `e=$(mktemp); pg_dump -U test -d ` + dbName + ` --schema-only --restrict-key=test` +
		` 2>"$e" || { c=$?; cat "$e"; exit $c; }`
	code, out, err := s.container.Exec(context.Background(), []string{"sh", "-c", cmd}, tcexec.Multiplexed())
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
	return string(dump), nil
}

func (s *testServer) adminExec(sql string) error {
	_, err := s.admin.Exec(context.Background(), sql)
	return err
}

// connStrFor returns the connection string of another database on the container
func (s *testServer) connStrFor(dbName string) string {
	u, err := url.Parse(s.baseConnStr)
	checkErr(err)
	u.Path = "/" + dbName
	return u.String()
}

// terminate stops the container, if it started; call it once every test has returned
func (s *testServer) terminate() {
	_ = testcontainers.TerminateContainer(s.container)
}
