package db

import (
	"context"
	"testing"

	"github.com/bcmk/siren/v2/lib/cmdlib"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

type testDB struct {
	*Database
	terminate func()
}

func newTestDB(t *testing.T) *testDB {
	t.Helper()
	cmdlib.Verbosity = cmdlib.SilentVerbosity
	ctx := context.Background()
	pgContainer, err := postgres.Run(
		ctx,
		"postgres:18",
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatal(err)
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}

	d := NewDatabase(connStr, false)
	d.ApplyMigrations()

	return &testDB{
		Database: &d,
		terminate: func() {
			_ = d.Close()
			_ = pgContainer.Terminate(ctx)
		},
	}
}
