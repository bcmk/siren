// Package db represents a database
package db

import (
	"database/sql"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bcmk/siren/lib/cmdlib"
)

var (
	checkErr = cmdlib.CheckErr
	linf     = cmdlib.Linf
)

// QueryDurationsData represents duration parameters of specific query
type QueryDurationsData struct {
	Avg   float64
	Count int
}

// Database represents a database and operatons with it
type Database struct {
	Durations      map[string]QueryDurationsData
	db             *sql.DB
	mainGID        int
	shouldCheckGID bool
}

// NewDatabase creates a new database object
func NewDatabase(connString string, shouldCheckGID bool) Database {
	db, err := sql.Open("postgres", connString)
	checkErr(err)
	return Database{
		Durations:      map[string]QueryDurationsData{},
		db:             db,
		shouldCheckGID: shouldCheckGID,
		mainGID:        gid(),
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
	if !d.shouldCheckGID {
		return
	}
	current := gid()
	if d.mainGID != current {
		checkErr(fmt.Errorf("database queries should be run from single thread, expected: %d, actual: %d", d.mainGID, current))
	}
}

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

// MustExec executes the query
func (d *Database) MustExec(query string, args ...interface{}) {
	defer d.Measure("db: " + query)()
	stmt, err := d.db.Prepare(query)
	checkErr(err)
	_, err = stmt.Exec(args...)
	checkErr(err)
	checkErr(stmt.Close())
}

// MustExecPrepared executes the prepared query
func (d *Database) MustExecPrepared(stmt *sql.Stmt, args ...interface{}) {
	d.checkTID()
	_, err := stmt.Exec(args...)
	checkErr(err)
}

// MustInt executes the query and returns single integer
func (d *Database) MustInt(query string, args ...interface{}) (result int) {
	defer d.Measure("db: " + query)()
	row := d.db.QueryRow(query, args...)
	checkErr(row.Scan(&result))
	return result
}

// MaybeRecord executes the query and returns single record on no records
func (d *Database) MaybeRecord(query string, args QueryParams, record ScanTo) bool {
	defer d.Measure("db: " + query)()
	row := d.db.QueryRow(query, args...)
	err := row.Scan(record...)
	if err == sql.ErrNoRows {
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
	query, err := d.db.Query(queryString, args...)
	checkErr(err)
	for query.Next() {
		checkErr(query.Scan(record...))
		store()
	}
	checkErr(query.Close())
}

// Begin begins a transaction
func (d *Database) Begin() (*sql.Tx, error) { return d.db.Begin() }

// Close closes a database
func (d *Database) Close() error { return d.db.Close() }

// Total returns total duration of the query
func (q QueryDurationsData) Total() float64 {
	return q.Avg * float64(q.Count)
}
