package db

import (
	"slices"
	"testing"
)

func TestClosestNicknames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		term      string
		nicknames []string
		want      []string
	}{
		{
			name: "no nicknames",
			term: "alice",
		},
		{
			name:      "each nickname once",
			term:      "alice",
			nicknames: []string{"alice1", "alice", "alice1", "alice"},
			want:      []string{"alice", "alice1"},
		},
		{
			// "a" sorts first and ranks last
			name: "the closest seven",
			term: "rank",
			nicknames: []string{
				"a", "rank1234567", "rank123456", "rank12345", "rank1234", "rank123", "rank12", "rank1", "rank",
			},
			want: []string{"rank", "rank1", "rank12", "rank123", "rank1234", "rank12345", "rank123456"},
		},
		{
			name:      "smith as a word before smith inside one",
			term:      "smith",
			nicknames: []string{"blacksmith", "xsmithx", "alice_smith", "john_smith"},
			want:      []string{"john_smith", "alice_smith", "xsmithx", "blacksmith"},
		},
		{
			name:      "anna as a second word between joanna and hannah",
			term:      "anna",
			nicknames: []string{"hannah", "sweet_anna", "joanna"},
			want:      []string{"joanna", "sweet_anna", "hannah"},
		},
		{
			name:      "a nickname shorter than the term",
			term:      "alice_smith",
			nicknames: []string{"alice_jones", "alice"},
			want:      []string{"alice", "alice_jones"},
		},
		{
			name:      "the same separator before another one",
			term:      "a_b",
			nicknames: []string{"a-b", "a_b"},
			want:      []string{"a_b", "a-b"},
		},
		{
			name:      "the same number of separators before another",
			term:      "a__b",
			nicknames: []string{"a_b", "a___b", "a__b"},
			want:      []string{"a__b", "a___b", "a_b"},
		},
		{
			// Enough nicknames for an unstable sort to reorder aba to abz, which are equally close
			name: "equally close nicknames in alphabetical order",
			term: "ab",
			nicknames: []string{
				"abz", "aby", "abx", "abw", "abv", "abu", "abt", "abs", "abr", "abq", "abp", "abo", "abn",
				"abm", "abl", "abk", "abj", "abi", "abh", "abg", "abf", "abe", "abd", "abc", "abb", "aba",
				"ab", "ab_q", "abxx", "xab", "abcdefgh",
			},
			want: []string{"ab", "ab_q", "aba", "abb", "abc", "abd", "abe"},
		},
		{
			name:      "no trigrams on either side",
			term:      "___",
			nicknames: []string{"____________", "a___"},
			want:      []string{"a___", "____________"},
		},
		{
			// Both are 29/15 away, reached through different trigram and edit counts
			name:      "equally close nicknames with different counts",
			term:      "anna",
			nicknames: []string{"xanna1", "hot_anna22"},
			want:      []string{"hot_anna22", "xanna1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := closestNicknames(tt.term, slices.Clone(tt.nicknames)); !slices.Equal(got, tt.want) {
				t.Errorf("closestNicknames(%q, %q) = %q, want %q", tt.term, tt.nicknames, got, tt.want)
			}
		})
	}
}

// The wanted counts are pg_trgm's:
// its <-> returns 1 - shared/union rounded to real, or 1 when union is 0.
func TestSharedTrigrams(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		a, b          string
		shared, union int
	}{
		{"the same string", "alice", "alice", 6, 6},
		{"nothing shared", "abc", "xyz", 0, 8},
		// "  a", " ab" shared of "  a", " ab", "abc", "bc " and "  a", " ab", "abd", "bd "
		{"a different last letter", "abc", "abd", 2, 6},
		// The start is padded with two spaces and the end with one
		{"a shared start counts for more than a shared end", "ab", "ax", 1, 5},
		{"a shared end", "ab", "xb", 0, 6},
		// "aaa" alone, which each string holds once however long its run
		{"a repeated letter", "aaaaa", "xaaaaay", 1, 9},
		// All six of smith's trigrams, of the eleven that john and smith give
		{"a word of the other string", "smith", "john_smith", 6, 11},
		{"inside a word of the other string", "smith", "blacksmith", 4, 13},
		{"separators do not count", "a_b", "a-b", 4, 4},
		{"nor does their number", "a_b", "a__b", 4, 4},
		{"no letters or digits", "___", "x___y", 0, 4},
		{"empty strings", "", "", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, pair := range [][2]string{{tt.a, tt.b}, {tt.b, tt.a}} {
				shared, union := sharedTrigrams(trigrams(pair[0]), trigrams(pair[1]))
				if shared != tt.shared || union != tt.union {
					t.Errorf("%q and %q share %d trigrams of %d, want %d of %d",
						pair[0], pair[1], shared, union, tt.shared, tt.union)
				}
			}
		})
	}
}

// The wanted distances are the ones PostgreSQL's levenshtein gives.
func TestLevenshtein(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "abcd", 1},
		{"abc", "bc", 1},
		{"kitten", "sitting", 3},
		{"a_b", "a__b", 1},
		{"smith", "john_smith", 5},
		{"wonderwxyz", "wonderland", 4},
	}
	for _, tt := range tests {
		t.Run(tt.a+" to "+tt.b, func(t *testing.T) {
			t.Parallel()
			if got := levenshtein(tt.a, tt.b); got != tt.want {
				t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
