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
	connStr, drop, err := clones.EmptyDatabase()
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
	defer fromSnapshot.terminate()

	replayed, replayedConnStr := newEmptyDatabase(t)
	replayed.ApplyMigrationsOneByOne()
	// The schema snapshot records every migration at time 0
	if n := replayed.MustInt(`select count(*) from schema_migrations where applied_at = 0`); n != 0 {
		t.Errorf("%d migrations came from the schema snapshot", n)
	}

	t.Run("schema", func(t *testing.T) {
		got, err := clones.PgDump(fromSnapshot.connStr, "--schema-only")
		if err != nil {
			t.Fatal(err)
		}
		want, err := clones.PgDump(replayedConnStr, "--schema-only")
		if err != nil {
			t.Fatal(err)
		}
		if diff := firstDifference(got, want); diff != "" {
			t.Errorf("the schema snapshot differs from the migrations: %s", diff)
		}
	})

	// No migration inserts rows; one that does must compare them here instead
	const tablesWithRows = `
		select tablename
		from pg_tables
		where schemaname = 'public'
			and tablename <> 'schema_migrations'
			and (xpath('/row/n/text()', query_to_xml(
				format('select count(*) as n from %I', tablename), false, true, '')))[1]::text::int > 0`
	for _, d := range []*Database{fromSnapshot.Database, replayed} {
		if tables := d.MustStrings(tablesWithRows); len(tables) > 0 {
			t.Errorf("rows in %q, want none", tables)
		}
	}

	queries := []struct {
		name string
		sql  string
	}{
		{name: "applied migrations", sql: `select name from schema_migrations order by name`},
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

func TestSchemaSnapshotStartsAnEmptyDatabase(t *testing.T) {
	t.Parallel()
	d, _ := newEmptyDatabase(t)
	const sessionSettings = `
		select coalesce(string_agg(name || ' = ' || setting, ', ' order by name), '')
		from pg_settings
		where source = 'session'`
	before := d.MustStrings(sessionSettings)[0]

	d.ApplyMigrations()

	// The schema snapshot records every migration at time 0
	if n := d.MustInt(`select count(*) from schema_migrations where applied_at <> 0`); n != 0 {
		t.Errorf("%d migrations ran instead of the schema snapshot", n)
	}
	// The connection stays in use
	if after := d.MustStrings(sessionSettings)[0]; after != before {
		t.Errorf("session settings changed from %q to %q", before, after)
	}
}

func TestMigratedDatabaseSkipsTheSchemaSnapshot(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)
	defer d.terminate()
	// This migration, a vacuum analyze, replays safely
	d.MustExec(`delete from schema_migrations where name = 'message_log_chat_id_indexes_rework_2'`)

	d.ApplyMigrations()

	if n := d.MustInt(`select count(*) from schema_migrations where applied_at <> 0`); n != 1 {
		t.Errorf("%d migrations ran, want the 1 missing", n)
	}
}

func TestNoTablesInPublic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		prepare string
		want    bool
	}{
		{name: "no tables", want: true},
		{name: "a table", prepare: "create table bystander (id integer)"},
		{
			name:    "a table in another schema",
			prepare: "create schema other; create table other.bystander (id integer)",
			want:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d, _ := newEmptyDatabase(t)
			if tt.prepare != "" {
				d.MustExec(tt.prepare)
			}
			if got := d.noTablesInPublic(); got != tt.want {
				t.Errorf("noTablesInPublic() = %v, want %v", got, tt.want)
			}
		})
	}
}
