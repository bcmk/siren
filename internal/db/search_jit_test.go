package db

import (
	"slices"
	"testing"
)

// TestSearchRankIsNotJITCompiled pins the rank statement below the JIT threshold
func TestSearchRankIsNotJITCompiled(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)
	defer d.terminate()
	d.MustExec("set pg_stat_statements.track = top")
	d.SearchStreamers("alice")
	got := d.MustInts(`
		select jit_functions
		from pg_stat_statements
		where dbid = (select oid from pg_database where datname = current_database())
			and query like '%search_streamers_rank%'`)
	if !slices.Equal(got, []int{0}) {
		t.Errorf("the rank statement's jit_functions = %v, want [0]", got)
	}
}
