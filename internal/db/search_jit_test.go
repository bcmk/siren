package db

import (
	"slices"
	"testing"
)

// A leg's planner settings must not cost a search statement past the JIT threshold.
func TestSearchIsNotJITCompiled(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)
	d.MustExec("set pg_stat_statements.track = top")
	// Every leg, then the prefix and the nonalnum-runs legs after another leg's settings
	for _, term := range []string{"alice", "aaaaaa", "___", "ab", "ab_cd", "ab___"} {
		d.SearchStreamers(term)
	}
	got := d.MustStrings(`
		select format('%s: %s calls, %s jit', name, sum(calls), max(jit_functions))
		from (
			select substring(query from 'name=''(\w+)''') as name, calls, jit_functions
			from pg_stat_statements
			where dbid = (select oid from pg_database where datname = current_database())
				and query like '%search_streamers_%'
		) statements
		group by name
		order by name`)
	want := []string{
		"search_streamers_exact: 6 calls, 0 jit",
		"search_streamers_infix: 1 calls, 0 jit",
		"search_streamers_nonalnum_runs: 2 calls, 0 jit",
		"search_streamers_prefix: 2 calls, 0 jit",
		"search_streamers_rank: 6 calls, 0 jit",
		"search_streamers_repeated_runs: 1 calls, 0 jit",
		"search_streamers_similarity: 4 calls, 0 jit",
	}
	if !slices.Equal(got, want) {
		t.Errorf("search statements = %q, want %q", got, want)
	}
}
