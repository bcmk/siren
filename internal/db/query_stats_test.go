package db

import "testing"

// TestResetQueryStats pins the reset to the current database,
// and checks that a failing one leaves startup alone
func TestResetQueryStats(t *testing.T) {
	t.Parallel()
	target := newTestDB(t)
	other := newTestDB(t)

	const markedQuery = "select count(*) from referrals /*reset_query_stats_marker*/"
	markedQueryCount := func(d *testDB) int {
		return d.MustInt(`
			select count(*)
			from pg_stat_statements
			where dbid = (select oid from pg_database where datname = current_database())
				and query like '%reset_query_stats_marker%'`)
	}
	for _, d := range []*testDB{target, other} {
		d.MustExec("set pg_stat_statements.track = top")
		d.MustInt(markedQuery)
	}
	if got := []int{markedQueryCount(target), markedQueryCount(other)}; got[0] != 1 || got[1] != 1 {
		t.Fatalf("counted %v before the reset, want both 1", got)
	}

	target.ResetQueryStats()

	if got := markedQueryCount(target); got != 0 {
		t.Errorf("counted %d in the reset database, want 0", got)
	}
	if got := markedQueryCount(other); got != 1 {
		t.Errorf("counted %d in another database, want 1", got)
	}

	// Must not panic
	target.MustExec("drop extension pg_stat_statements")
	target.ResetQueryStats()
}

// TestQueryDurations pins what the bot's /performance reads
func TestQueryDurations(t *testing.T) {
	t.Parallel()
	d := Database{durations: map[string]QueryDurationsData{}}
	d.Measure("a query")()
	if got := d.QueryDurations()["a query"].Count; got != 1 {
		t.Errorf("count = %d, want 1", got)
	}
}
