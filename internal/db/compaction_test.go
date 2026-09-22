package db

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/bcmk/siren/v5/lib/cmdlib"
)

type compactionInsert struct {
	ts     int
	status cmdlib.StatusKind
}

func insertHistory(d *Database, nickname string, inserts []compactionInsert) {
	for _, ins := range inserts {
		d.UpsertUnconfirmedStatusChanges([]StatusChange{{Nickname: nickname, Status: ins.status}}, ins.ts)
	}
}

const day = 24 * 60 * 60

// vacuumQueued vacuums every queued chunk and returns how many it vacuumed
func vacuumQueued(ctx context.Context, d *Database) (int, error) {
	vacuumed := 0
	for {
		n, err := d.VacuumNextStatusChangesChunk(ctx, true)
		if err != nil || n == 0 {
			return vacuumed, err
		}
		vacuumed += n
	}
}

// queueChunk queues the chunk holding ts for a vacuum, or a rewrite, in a transaction of its own
func queueChunk(t *testing.T, d *Database, ts int, rewrite bool) {
	t.Helper()
	ctx := context.Background()
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	chunks, err := d.StatusChangesChunks(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	var vacuums []StatusChangesVacuum
	for _, c := range chunks {
		if c.Start <= ts && ts < c.End {
			vacuums = append(vacuums, StatusChangesVacuum{Chunk: c.Name, Rewrite: rewrite})
		}
	}
	if err := d.QueueStatusChangesVacuums(ctx, tx, vacuums); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestVacuumStatusChangesChunkCleansIndexes pins the vacuum's index pass:
// PostgreSQL skips it under 2% of pages with dead rows and leaves those pages not all-visible
func TestVacuumStatusChangesChunkCleansIndexes(t *testing.T) {
	t.Parallel()
	const start = 140 * day
	db := newTestDB(t)
	defer db.terminate()
	insertHistory(db.Database, "a", []compactionInsert{{start, cmdlib.StatusOnline}})
	db.MustExec(`
		insert into status_changes (timestamp, streamer_id, status, prev_status)
		select
			$1::integer + s,
			$2,
			case when s % 2 = 1 then $3::smallint else $4 end,
			case when s % 2 = 1 then $4::smallint else $3 end
		from generate_series(1, 300000) s`,
		start, streamerID(t, db.Database, "a"), cmdlib.StatusOffline, cmdlib.StatusOnline)
	ctx := context.Background()
	queueChunk(t, db.Database, start, false)
	if _, err := vacuumQueued(ctx, db.Database); err != nil {
		t.Fatal(err)
	}
	// Dead rows on about a dozen of the chunk's pages, under 1%
	db.MustExec("delete from status_changes where timestamp between $1 and $2", start+1000, start+3000)
	queueChunk(t, db.Database, start, false)
	if _, err := vacuumQueued(ctx, db.Database); err != nil {
		t.Fatal(err)
	}
	var pages, visible int
	db.MustQuery(`
		select relpages, relallvisible
		from pg_class
		where oid = (
			select format('%I.%I', chunk_schema, chunk_name)::regclass
			from timescaledb_information.chunks
			where hypertable_name = 'status_changes' and range_start_integer = $1
		)`,
		QueryParams{start},
		ScanTo{&pages, &visible},
		func() {})
	if pages < 1000 || visible != pages {
		t.Errorf("%d of %d pages all-visible, want all of over 1000", visible, pages)
	}
}

// TestVacuumQueue pins the vacuum queue: each chunk once, vacuumed in the order queued,
// and kept while a migration holds the lock
func TestVacuumQueue(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	// Rows in chunks 19 and 24
	insertHistory(db.Database, "a", []compactionInsert{
		{136 * day, cmdlib.StatusOnline},
		{171 * day, cmdlib.StatusOffline},
	})
	other := NewDatabase(db.connStr, false, 5)
	defer func() { _ = other.Close() }()
	ctx := context.Background()

	// Chunk 24, then 19, then 24 again
	queueChunk(t, db.Database, 171*day, false)
	queueChunk(t, db.Database, 136*day, false)
	queueChunk(t, db.Database, 171*day, false)
	if got := queuedChunks(db.Database); !slices.Equal(got, []int{24, 19}) {
		t.Fatalf("queued %v, want [24 19]", got)
	}
	db.MustExec("select pg_advisory_lock($1)", migrationLock)
	if n, err := other.VacuumNextStatusChangesChunk(ctx, true); n != 0 || err != nil {
		t.Errorf("under a migration: vacuumed %d with %v, want 0", n, err)
	}
	db.MustExec("select pg_advisory_unlock($1)", migrationLock)
	if got := queuedChunks(db.Database); !slices.Equal(got, []int{24, 19}) {
		t.Errorf("queued %v after a migration, want [24 19]", got)
	}
	for i, want := range [][]int{{19}, nil} {
		if n, err := other.VacuumNextStatusChangesChunk(ctx, true); n != 1 || err != nil {
			t.Fatalf("vacuum %d: vacuumed %d with %v, want 1", i, n, err)
		}
		if got := queuedChunks(db.Database); !slices.Equal(got, want) {
			t.Errorf("queued %v after vacuum %d, want %v", got, i, want)
		}
	}
	if n, err := other.VacuumNextStatusChangesChunk(ctx, true); n != 0 || err != nil {
		t.Errorf("an empty queue: vacuumed %d with %v, want 0", n, err)
	}
}

// TestVacuumNextStatusChangesChunkDequeuesAGoneChunk pins a chunk dropped since it was queued:
// it leaves the queue and counts no vacuum
func TestVacuumNextStatusChangesChunkDequeuesAGoneChunk(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	insertHistory(db.Database, "a", []compactionInsert{{140 * day, cmdlib.StatusOnline}})
	queueChunk(t, db.Database, 140*day, false)
	db.MustExec("truncate status_changes")
	ctx := context.Background()
	if n, err := db.VacuumNextStatusChangesChunk(ctx, true); n != 0 || err != nil {
		t.Errorf("vacuumed %d with %v, want 0", n, err)
	}
	if n, err := db.QueuedStatusChangesVacuums(ctx); n != 0 || err != nil {
		t.Errorf("%d queued with %v, want 0", n, err)
	}
}

// TestQueueStatusChangesVacuumsUpgradesToARewrite pins a chunk queued again:
// it keeps its place, rewritten if either asks
func TestQueueStatusChangesVacuumsUpgradesToARewrite(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	// Rows in chunks 19 and 24
	insertHistory(db.Database, "a", []compactionInsert{
		{136 * day, cmdlib.StatusOnline},
		{171 * day, cmdlib.StatusOffline},
	})
	rewrites := func() []bool {
		var all []bool
		var rewrite bool
		db.MustQuery(
			"select rewrite from status_changes_vacuum_queue order by id",
			nil,
			ScanTo{&rewrite},
			func() { all = append(all, rewrite) })
		return all
	}
	queueChunk(t, db.Database, 171*day, false)
	queueChunk(t, db.Database, 136*day, false)
	queueChunk(t, db.Database, 171*day, true)
	if got, rewritten := queuedChunks(db.Database), rewrites(); !slices.Equal(got, []int{24, 19}) ||
		!slices.Equal(rewritten, []bool{true, false}) {
		t.Errorf("queued %v rewriting %v, want [24 19] rewriting [true false]", got, rewritten)
	}
	queueChunk(t, db.Database, 171*day, false)
	if rewritten := rewrites(); !slices.Equal(rewritten, []bool{true, false}) {
		t.Errorf("rewriting %v after a vacuum queued again, want [true false]", rewritten)
	}
}

// TestVacuumNextStatusChangesChunkRewrites pins a rewrite:
// it releases the space of rows deleted all over the chunk, which a vacuum leaves in place,
// and the vacuum after it leaves every page all-visible
func TestVacuumNextStatusChangesChunkRewrites(t *testing.T) {
	t.Parallel()
	const start = 140 * day
	db := newTestDB(t)
	defer db.terminate()
	insertHistory(db.Database, "a", []compactionInsert{{start, cmdlib.StatusOnline}})
	db.MustExec(`
		insert into status_changes (timestamp, streamer_id, status, prev_status)
		select
			$1::integer + s,
			$2,
			case when s % 2 = 1 then $3::smallint else $4 end,
			case when s % 2 = 1 then $4::smallint else $3 end
		from generate_series(1, 100000) s`,
		start, streamerID(t, db.Database, "a"), cmdlib.StatusOffline, cmdlib.StatusOnline)
	ctx := context.Background()
	chunkPages := func() (pages, visible int) {
		db.MustQuery(`
			select relpages, relallvisible
			from pg_class
			where oid = (
				select format('%I.%I', chunk_schema, chunk_name)::regclass
				from timescaledb_information.chunks
				where hypertable_name = 'status_changes' and range_start_integer = $1
			)`,
			QueryParams{start},
			ScanTo{&pages, &visible},
			func() {})
		return pages, visible
	}
	// Half the rows, on every page
	db.MustExec("delete from status_changes where timestamp > $1 and (timestamp - $1) % 4 < 2", start)
	queueChunk(t, db.Database, start, false)
	if _, err := vacuumQueued(ctx, db.Database); err != nil {
		t.Fatal(err)
	}
	vacuumed, _ := chunkPages()
	queueChunk(t, db.Database, start, true)
	if _, err := vacuumQueued(ctx, db.Database); err != nil {
		t.Fatal(err)
	}
	if pages, visible := chunkPages(); pages > vacuumed*6/10 || visible != pages {
		t.Errorf("%d pages after the rewrite, %d of them all-visible, %d after the vacuum, want all visible and about half",
			pages, visible, vacuumed)
	}
}

// chunkName returns the name of the chunk starting at start, quoted for a statement
func chunkName(d *Database, start int) string {
	var name string
	d.MustQuery(`
		select format('%I.%I', chunk_schema, chunk_name)
		from timescaledb_information.chunks
		where hypertable_name = 'status_changes' and range_start_integer = $1`,
		QueryParams{start},
		ScanTo{&name},
		func() {})
	return name
}

// TestVacuumNextStatusChangesChunkSendsAHeldRewriteBack pins a rewrite of a chunk a reader holds:
// it gives up at once, sending the chunk behind those queued after it, which go on meanwhile,
// and once the reader ends the rewrite goes through
func TestVacuumNextStatusChangesChunkSendsAHeldRewriteBack(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	// Rows in chunks 20 and 24
	insertHistory(db.Database, "a", []compactionInsert{
		{140 * day, cmdlib.StatusOnline},
		{171 * day, cmdlib.StatusOffline},
	})
	queueChunk(t, db.Database, 140*day, true)
	queueChunk(t, db.Database, 171*day, false)
	other := NewDatabase(db.connStr, false, 5)
	defer func() { _ = other.Close() }()
	ctx := context.Background()

	reader, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Rollback(ctx) }()
	if _, err := reader.Exec(ctx, "select count(*) from "+chunkName(db.Database, 140*day)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if n, err := other.VacuumNextStatusChangesChunk(ctx, true); n != 0 || err != nil || time.Since(start) > 2*time.Second {
		t.Errorf("under a reader: vacuumed %d with %v in %v, want 0 at once", n, err, time.Since(start))
	}
	if got := queuedChunks(db.Database); !slices.Equal(got, []int{24, 20}) {
		t.Errorf("queued %v under a reader, want [24 20]", got)
	}
	if n, err := other.VacuumNextStatusChangesChunk(ctx, true); n != 1 || err != nil {
		t.Errorf("vacuumed %d with %v behind the held chunk, want 1", n, err)
	}
	if err := reader.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := other.VacuumNextStatusChangesChunk(ctx, true); n != 1 || err != nil {
		t.Errorf("vacuumed %d with %v once the reader ended, want 1", n, err)
	}
}

// TestVacuumNextStatusChangesChunkSendsAHeldVacuumBack pins a vacuum's lock timeout:
// a session holding the chunk past it sends the chunk to the back of the queue,
// and the timeout is reset after
func TestVacuumNextStatusChangesChunkSendsAHeldVacuumBack(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	// Rows in chunks 20 and 24
	insertHistory(db.Database, "a", []compactionInsert{
		{140 * day, cmdlib.StatusOnline},
		{171 * day, cmdlib.StatusOffline},
	})
	queueChunk(t, db.Database, 140*day, false)
	queueChunk(t, db.Database, 171*day, false)
	other := NewDatabase(db.connStr, false, 5)
	defer func() { _ = other.Close() }()
	ctx := context.Background()

	holder, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	lock := "lock table " + chunkName(db.Database, 140*day) + " in share update exclusive mode"
	if _, err := holder.Exec(ctx, lock); err != nil {
		t.Fatal(err)
	}
	// Fails rather than hangs should the lock timeout go
	other.MustExec("set statement_timeout = '10s'")
	if n, err := other.VacuumNextStatusChangesChunk(ctx, true); n != 0 || err != nil {
		t.Errorf("under a holder: vacuumed %d with %v, want 0", n, err)
	}
	if got := queuedChunks(db.Database); !slices.Equal(got, []int{24, 20}) {
		t.Errorf("queued %v under a holder, want [24 20]", got)
	}
	var timeout string
	other.MustQuery("show lock_timeout", nil, ScanTo{&timeout}, func() {})
	if timeout != "0" {
		t.Errorf("lock_timeout = %s after the vacuum, want 0", timeout)
	}
}

// TestVacuumNextStatusChangesChunkRewritesOnlyWhileARuleDoes pins a queued rewrite
// once no rule rewrites: the chunk gets a plain vacuum, keeping its file
func TestVacuumNextStatusChangesChunkRewritesOnlyWhileARuleDoes(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	insertHistory(db.Database, "a", []compactionInsert{{140 * day, cmdlib.StatusOnline}})
	queueChunk(t, db.Database, 140*day, true)
	file := func() int {
		return db.MustInt("select pg_relation_filenode($1::text::regclass)::bigint", chunkName(db.Database, 140*day))
	}
	before := file()
	if n, err := db.VacuumNextStatusChangesChunk(context.Background(), false); n != 1 || err != nil {
		t.Errorf("vacuumed %d with %v, want 1", n, err)
	}
	if after := file(); after != before {
		t.Errorf("file %d after the vacuum, %d before, want it kept", after, before)
	}
}

// TestVacuumNextStatusChangesChunkRequeuesARewriteAsAVacuum pins a rewrite leaving dead rows
// to an older snapshot: the chunk goes back to the queue for a plain vacuum
func TestVacuumNextStatusChangesChunkRequeuesARewriteAsAVacuum(t *testing.T) {
	t.Parallel()
	const start = 140 * day
	db := newTestDB(t)
	defer db.terminate()
	insertHistory(db.Database, "a", []compactionInsert{{start, cmdlib.StatusOnline}})
	db.MustExec(`
		insert into status_changes (timestamp, streamer_id, status, prev_status)
		select
			$1::integer + s,
			$2,
			case when s % 2 = 1 then $3::smallint else $4 end,
			case when s % 2 = 1 then $4::smallint else $3 end
		from generate_series(1, 3000) s`,
		start, streamerID(t, db.Database, "a"), cmdlib.StatusOffline, cmdlib.StatusOnline)
	other := NewDatabase(db.connStr, false, 5)
	defer func() { _ = other.Close() }()
	ctx := context.Background()
	reader, err := other.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Rollback(ctx) }()
	// A snapshot taken before the delete, on another table, so the rewrite can lock the chunk
	if _, err := reader.Exec(ctx, "set transaction isolation level repeatable read"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Exec(ctx, "select count(*) from streamers"); err != nil {
		t.Fatal(err)
	}
	db.MustExec("delete from status_changes where timestamp between $1 and $2", start+1000, start+2000)
	queueChunk(t, db.Database, start, true)
	if n, err := db.VacuumNextStatusChangesChunk(ctx, true); n != 1 || err != nil {
		t.Fatalf("vacuumed %d with %v, want 1", n, err)
	}
	var rewrites []bool
	var rewrite bool
	db.MustQuery("select rewrite from status_changes_vacuum_queue", nil, ScanTo{&rewrite}, func() {
		rewrites = append(rewrites, rewrite)
	})
	if !slices.Equal(rewrites, []bool{false}) {
		t.Errorf("queued rewriting %v, want [false]", rewrites)
	}
}

// queuedChunks returns the queued chunks' numbers on the test database's 7-day grid, in queue order
func queuedChunks(d *Database) []int {
	var numbers []int
	var number int
	d.MustQuery(`
		select c.range_start_integer / $1
		from status_changes_vacuum_queue q
		join timescaledb_information.chunks c on format('%I.%I', c.chunk_schema, c.chunk_name) = q.chunk
		order by q.id`,
		QueryParams{7 * day},
		ScanTo{&number},
		func() { numbers = append(numbers, number) })
	return numbers
}

// TestVacuumNextStatusChangesChunkRequeuesRowsASnapshotSees pins the requeue:
// a vacuum leaving dead rows to an older snapshot sends the chunk to the back of the queue,
// and once the snapshot ends a later vacuum dequeues it with every page all-visible
func TestVacuumNextStatusChangesChunkRequeuesRowsASnapshotSees(t *testing.T) {
	t.Parallel()
	const start = 140 * day
	db := newTestDB(t)
	defer db.terminate()
	insertHistory(db.Database, "a", []compactionInsert{{start, cmdlib.StatusOnline}})
	// Chunk 24 for the queue behind chunk 20
	insertHistory(db.Database, "b", []compactionInsert{{171 * day, cmdlib.StatusOnline}})
	db.MustExec(`
		insert into status_changes (timestamp, streamer_id, status, prev_status)
		select
			$1::integer + s,
			$2,
			case when s % 2 = 1 then $3::smallint else $4 end,
			case when s % 2 = 1 then $4::smallint else $3 end
		from generate_series(1, 30000) s`,
		start, streamerID(t, db.Database, "a"), cmdlib.StatusOffline, cmdlib.StatusOnline)
	ctx := context.Background()
	queueChunk(t, db.Database, start, false)
	if _, err := vacuumQueued(ctx, db.Database); err != nil {
		t.Fatal(err)
	}
	other := NewDatabase(db.connStr, false, 5)
	defer func() { _ = other.Close() }()
	reader, err := other.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Rollback(ctx) }()
	// A snapshot taken before the delete
	if _, err := reader.Exec(ctx, "set transaction isolation level repeatable read"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Exec(ctx, "select count(*) from status_changes"); err != nil {
		t.Fatal(err)
	}
	db.MustExec("delete from status_changes where timestamp between $1 and $2", start+1000, start+3000)
	queueChunk(t, db.Database, start, false)
	queueChunk(t, db.Database, 171*day, false)
	if n, err := db.VacuumNextStatusChangesChunk(ctx, true); n != 1 || err != nil {
		t.Fatalf("vacuumed %d with %v, want 1", n, err)
	}
	if got := queuedChunks(db.Database); !slices.Equal(got, []int{24, 20}) {
		t.Errorf("queued %v while the snapshot sees the rows, want [24 20]", got)
	}
	if err := reader.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := vacuumQueued(ctx, db.Database); err != nil {
		t.Fatal(err)
	}
	if got := queuedChunks(db.Database); len(got) != 0 {
		t.Errorf("queued %v after the snapshot, want none", got)
	}
	var pages, visible int
	db.MustQuery(`
		select relpages, relallvisible
		from pg_class
		where oid = (
			select format('%I.%I', chunk_schema, chunk_name)::regclass
			from timescaledb_information.chunks
			where hypertable_name = 'status_changes' and range_start_integer = $1
		)`,
		QueryParams{start},
		ScanTo{&pages, &visible},
		func() {})
	if visible != pages {
		t.Errorf("%d of %d pages all-visible, want all", visible, pages)
	}
}

// TestVacuumNextStatusChangesChunkFlushesStatsFirst pins the stats flush before a vacuum:
// deletes made on the vacuum's connection just before it, if counted after it,
// would requeue the chunk it cleaned
func TestVacuumNextStatusChangesChunkFlushesStatsFirst(t *testing.T) {
	t.Parallel()
	const start = 140 * day
	db := newTestDB(t)
	defer db.terminate()
	insertHistory(db.Database, "a", []compactionInsert{{start, cmdlib.StatusOnline}})
	db.MustExec(`
		insert into status_changes (timestamp, streamer_id, status, prev_status)
		select
			$1::integer + s,
			$2,
			case when s % 2 = 1 then $3::smallint else $4 end,
			case when s % 2 = 1 then $4::smallint else $3 end
		from generate_series(1, 30000) s`,
		start, streamerID(t, db.Database, "a"), cmdlib.StatusOffline, cmdlib.StatusOnline)
	ctx := context.Background()
	queueChunk(t, db.Database, start, false)
	if _, err := vacuumQueued(ctx, db.Database); err != nil {
		t.Fatal(err)
	}
	// A vacuum past the second a connection waits between its stats flushes
	db.MustExec("set vacuum_cost_delay = '20ms'")
	db.MustExec("set vacuum_cost_limit = 1")
	db.MustExec("delete from status_changes where timestamp between $1 and $2", start+1000, start+1100)
	queueChunk(t, db.Database, start, false)
	if n, err := db.VacuumNextStatusChangesChunk(ctx, true); n != 1 || err != nil {
		t.Fatalf("vacuumed %d with %v, want 1", n, err)
	}
	if n, err := db.QueuedStatusChangesVacuums(ctx); n != 0 || err != nil {
		t.Errorf("%d queued with %v after a clean vacuum, want 0", n, err)
	}
}

// TestLockStatusChangesSkipsAMigration pins the migration lock:
// the lock fails while another session holds it, and succeeds afterwards
func TestLockStatusChangesSkipsAMigration(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	migrator := NewDatabase(db.connStr, false, 5)
	defer func() { _ = migrator.Close() }()
	ctx := context.Background()
	lock := func() error {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		return db.LockStatusChanges(ctx, tx)
	}

	migrator.lockMigrations()
	if err := lock(); !errors.Is(err, ErrMigrating) {
		t.Errorf("under the migration lock: %v, want ErrMigrating", err)
	}
	migrator.unlockMigrations()
	if err := lock(); err != nil {
		t.Errorf("after the migration: %v, want nil", err)
	}
}
