// Package db represents a database
package db

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bcmk/siren/lib/cmdlib"
	"github.com/jackc/pgx/v5"
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
	db             *pgx.Conn
	mainGID        int
	shouldCheckGID bool
}

// NewDatabase creates a new database object
func NewDatabase(connString string, shouldCheckGID bool) Database {
	db, err := pgx.Connect(context.Background(), connString)
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
	_, err := d.db.Exec(context.Background(), query, args...)
	checkErr(err)
}

// MustInt executes the query and returns single integer
func (d *Database) MustInt(query string, args ...interface{}) (result int) {
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
