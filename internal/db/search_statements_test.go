package db

import (
	"slices"
	"testing"
)

// Each search statement keeps one pg_stat_statements entry and is not JIT-compiled,
// and the searches leave the session's settings as they were.
func TestSearchStatements(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)
	d.MustExec("set pg_stat_statements.track = top")
	// Every leg at least twice, the prefix and nonalnum-runs legs once after another leg's settings
	for _, term := range []string{"alice", "aaaaaa", "___", "ab", "ab_cd", "ab___", "bobalice", "bbbbbb"} {
		d.SearchStreamers(term)
	}
	// One row per entry, so a statement with an entry per search breaks the want list
	got := d.MustStrings(`
		select format('%s: %s calls, %s jit', substring(query from 'name=''(\w+)'''), calls, jit_functions)
		from pg_stat_statements
		where dbid = (select oid from pg_database where datname = current_database())
			and query like '%search_streamers_%'
		order by 1`)
	want := []string{
		"search_streamers_exact: 8 calls, 0 jit",
		"search_streamers_infix: 2 calls, 0 jit",
		"search_streamers_nonalnum_runs: 2 calls, 0 jit",
		"search_streamers_prefix: 2 calls, 0 jit",
		"search_streamers_repeated_runs: 2 calls, 0 jit",
		"search_streamers_similarity: 6 calls, 0 jit",
	}
	if !slices.Equal(got, want) {
		t.Errorf("search statements = %q, want %q", got, want)
	}
	// The legs' settings end with their search's batch
	left := d.MustStrings(`
		select name from pg_settings
		where name in ('enable_seqscan', 'enable_indexscan', 'enable_indexonlyscan', 'enable_bitmapscan')
			and setting != 'on'`)
	if len(left) > 0 {
		t.Errorf("the searches left %q off", left)
	}
}
