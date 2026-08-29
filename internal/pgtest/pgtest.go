// Package pgtest starts the PostgreSQL server tests and schema-dump apply the migrations to
package pgtest

import (
	"context"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
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
