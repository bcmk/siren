// Package db represents a database
package db

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bcmk/siren/v3/lib/cmdlib"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	checkErr = cmdlib.CheckErr
	linf     = cmdlib.Linf
	lerr     = cmdlib.Lerr
)

// QueryDurationsData represents duration parameters of specific query
type QueryDurationsData struct {
	Avg   float64
	Count int
}

// Database represents a database and operatons with it
type Database struct {
	Durations         map[string]QueryDurationsData
	db                *pgx.Conn
	mainGID           int
	shouldCheckGID    bool
	gidCheckSuspended bool
	defaultMaxSubs    int
}

// NewDatabase creates a new database object.
// defaultMaxSubs is the limit for a user row auto-created by EnsureUser.
func NewDatabase(connString string, shouldCheckGID bool, defaultMaxSubs int) Database {
	config, err := pgx.ParseConfig(connString)
	checkErr(err)
	// Surface server notices (e.g. migration raise notice) through our logger.
	config.OnNotice = func(_ *pgconn.PgConn, n *pgconn.Notice) {
		linf("db: %s", n.Message)
	}
	db, err := pgx.ConnectConfig(context.Background(), config)
	checkErr(err)
	return Database{
		Durations:      map[string]QueryDurationsData{},
		db:             db,
		shouldCheckGID: shouldCheckGID,
		mainGID:        gid(),
		defaultMaxSubs: defaultMaxSubs,
	}
}

// QueryParams represents query parameters
type QueryParams []interface{}

// ScanTo represents scanning parameters
type ScanTo []interface{}

func gid() int {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	idField := strings.Fields(strings.TrimPrefix(string(buf[:n]), "goroutine "))[0]
	id, err := strconv.Atoi(idField)
	if err != nil {
		checkErr(fmt.Errorf("cannot get goroutine id: %v", err))
	}
	return id
}

func (d *Database) checkTID() {
	if !d.shouldCheckGID || d.gidCheckSuspended {
		return
	}
	current := gid()
	if d.mainGID != current {
		checkErr(fmt.Errorf("database queries should be run from single thread, expected: %d, actual: %d", d.mainGID, current))
	}
}

// SuspendGIDCheck and ResumeGIDCheck bracket startup:
// the database is created and initialized on a dedicated goroutine,
// off the main loop, while the loop's DB arms stay dormant.
// That goroutine alone touches the connection then,
// a coordinated handoff the single-goroutine check would otherwise reject.
func (d *Database) SuspendGIDCheck() { d.gidCheckSuspended = true }

// ResumeGIDCheck restores the check after the startup handoff.
func (d *Database) ResumeGIDCheck() { d.gidCheckSuspended = false }

// Measure measures query duration
func (d *Database) Measure(query string) func() {
	now := time.Now()
	d.checkTID()
	return func() {
		elapsed := time.Since(now).Seconds()
		data := d.Durations[query]
		data.Avg = (data.Avg*float64(data.Count) + elapsed) / float64(data.Count+1)
		data.Count++
		d.Durations[query] = data
	}
}

// MustExec executes the query and returns rows affected.
func (d *Database) MustExec(query string, args ...interface{}) int64 {
	defer d.Measure("db: " + query)()
	tag, err := d.db.Exec(context.Background(), query, args...)
	checkErr(err)
	return tag.RowsAffected()
}

// MustExecScript executes a SQL script using simple protocol.
// Simple protocol allows commands like VACUUM that cannot run inside a transaction.
func (d *Database) MustExecScript(script string) {
	_, err := d.db.PgConn().Exec(context.Background(), script).ReadAll()
	checkErr(err)
}

// ResetQueryStats clears the pg_stat_statements counters
// so its numbers cover only the current process's lifetime.
// The reset is scoped to the current database via the dbid argument
// (pg_stat_statements counters are cluster-wide otherwise).
// It is best-effort: the extension must be created
// (see the pg_stat_statements migration)
// and loaded via shared_preload_libraries,
// and a reset failure must never take down startup.
func (d *Database) ResetQueryStats() {
	const query = `
		select pg_stat_statements_reset(
			0,
			(select oid from pg_database where datname = current_database()),
			0)`
	if _, err := d.db.Exec(context.Background(), query); err != nil {
		lerr("cannot reset pg_stat_statements: %v", err)
		return
	}
	linf("reset pg_stat_statements counters for the current database")
}

// MustInt executes the query and returns single integer
func (d *Database) MustInt(query string, args ...interface{}) (result int) {
	defer d.Measure("db: " + query)()
	row := d.db.QueryRow(context.Background(), query, args...)
	checkErr(row.Scan(&result))
	return result
}

// MustBool executes the query and returns single boolean
func (d *Database) MustBool(query string, args ...interface{}) (result bool) {
	defer d.Measure("db: " + query)()
	row := d.db.QueryRow(context.Background(), query, args...)
	checkErr(row.Scan(&result))
	return result
}

// MaybeRecord executes the query and returns single record on no records
func (d *Database) MaybeRecord(query string, args QueryParams, record ScanTo) bool {
	defer d.Measure("db: " + query)()
	row := d.db.QueryRow(context.Background(), query, args...)
	err := row.Scan(record...)
	if err == pgx.ErrNoRows {
		return false
	}
	checkErr(err)
	return true
}

// MustStrings executes the query and returns strings arrays
func (d *Database) MustStrings(queryString string, args ...interface{}) (result []string) {
	var current string
	d.MustQuery(queryString, args, ScanTo{&current}, func() { result = append(result, current) })
	return
}

// MustQuery executes the query and stores data using store function
func (d *Database) MustQuery(queryString string, args QueryParams, record ScanTo, store func()) {
	defer d.Measure("db: " + queryString)()
	query, err := d.db.Query(context.Background(), queryString, args...)
	checkErr(err)
	for query.Next() {
		checkErr(query.Scan(record...))
		store()
	}
	query.Close()
}

// Begin begins a transaction
func (d *Database) Begin() (pgx.Tx, error) { return d.db.Begin(context.Background()) }

// SendBatch sends a batch
func (d *Database) SendBatch(batch *pgx.Batch) {
	conn := d.db.SendBatch(context.Background(), batch)
	checkErr(conn.Close())
}

// Close closes a database
func (d *Database) Close() error { return d.db.Close(context.Background()) }

// Total returns total duration of the query
func (q QueryDurationsData) Total() float64 {
	return q.Avg * float64(q.Count)
}
