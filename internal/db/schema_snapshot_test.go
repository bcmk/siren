package db

import (
	"strings"
	"testing"
)

// firstDifference names the first line two texts differ on, empty if none
func firstDifference(a, b string) string {
	as, bs := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range min(len(as), len(bs)) {
		if as[i] != bs[i] {
			return "line " + as[i] + "\nagainst " + bs[i]
		}
	}
	if len(as) != len(bs) {
		return "a different number of lines"
	}
	return ""
}

// newEmptyDatabase returns a database with no tables, and its connection string
func newEmptyDatabase(t *testing.T) (*Database, string) {
	t.Helper()
	connStr, drop, err := server.createEmptyDatabase()
	if err != nil {
		t.Fatal(err)
	}
	d := NewDatabase(connStr, false, 5)
	t.Cleanup(func() {
		_ = d.Close()
		if err := drop(); err != nil {
			t.Error(err)
		}
	})
	return &d, connStr
}

// TestSchemaSnapshotMatchesMigrations holds a database started from the schema snapshot
// to one that replays every migration.
func TestSchemaSnapshotMatchesMigrations(t *testing.T) {
	t.Parallel()
	fromSnapshot := newTestDB(t)

	replayed, _ := newEmptyDatabase(t)
	replayed.ApplyMigrationsOneByOne()
	// The schema snapshot records every migration at time 0
	if n := replayed.MustInt(`select count(*) from schema_migrations where applied_at = 0`); n != 0 {
		t.Errorf("%d migrations came from the schema snapshot", n)
	}

	t.Run("schema", func(t *testing.T) {
		got, err := server.pgDumpSchema(fromSnapshot.CurrentDatabase())
		if err != nil {
			t.Fatal(err)
		}
		want, err := server.pgDumpSchema(replayed.CurrentDatabase())
		if err != nil {
			t.Fatal(err)
		}
		if diff := firstDifference(got, want); diff != "" {
			t.Errorf("the schema snapshot differs from the migrations: %s", diff)
		}
	})

	// No migration leaves a row outside schema_migrations; one that does must compare them here
	for _, d := range []*Database{fromSnapshot.Database, replayed} {
		tables := d.MustStrings(`
			select format('public.%I', tablename)
			from pg_tables
			where schemaname = 'public' and tablename <> 'schema_migrations'`)
		for _, table := range tables {
			if n := d.MustInt("select count(*) from " + table); n != 0 {
				t.Errorf("%d rows in %s, want none", n, table)
			}
		}
	}

	// The schema snapshot names no owner, so the role running it owns everything
	if strings.Contains(schemaSnapshot, "OWNER TO") {
		t.Error("the schema snapshot names an owner")
	}

	queries := []struct {
		name string
		sql  string
	}{
		{name: "applied migrations", sql: `select name from schema_migrations order by name`},
		// Not in --schema-only
		{name: "sequences", sql: `
			select sequencename || ' ' || coalesce(last_value::text, 'unread')
			from pg_sequences
			where schemaname = 'public'
			order by sequencename`},
		// Not in --schema-only: TimescaleDB keeps these in catalog rows.
		// A replay creates and drops hypertables, so their ids differ.
		{name: "hypertables", sql: `
			select (to_jsonb(h) - 'id' - 'associated_table_prefix')::text
			from _timescaledb_catalog.hypertable h
			order by table_name`},
		{name: "dimensions", sql: `
			select h.table_name || ' ' || (to_jsonb(d) - 'id' - 'hypertable_id')::text
			from _timescaledb_catalog.dimension d
			join _timescaledb_catalog.hypertable h on h.id = d.hypertable_id
			order by h.table_name, d.column_name`},
		{name: "jobs", sql: `
			select to_jsonb(j)::text
			from _timescaledb_config.bgw_job j
			order by application_name`},
		// Not in a dump without --create, and a clone does not inherit them
		{name: "database settings", sql: `
			select coalesce(s.setconfig::text, '')
			from pg_db_role_setting s
			join pg_database d on d.oid = s.setdatabase
			where d.datname = current_database()`},
	}
	for _, tc := range queries {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(fromSnapshot.MustStrings(tc.sql), "\n")
			want := strings.Join(replayed.MustStrings(tc.sql), "\n")
			if diff := firstDifference(got, want); diff != "" {
				t.Errorf("the schema snapshot differs from the migrations: %s", diff)
			}
		})
	}
}
