package db

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
)

// maxSearchResults is the most nicknames a search returns
const maxSearchResults = 7

// closestNicknames returns up to maxSearchResults nicknames closest to the search term,
// closest first, each once, sorting nicknames in place.
// It ranks by twice the trigram distance
// plus the Levenshtein distance divided by the length of the longer of the nickname and the term.
func closestNicknames(term string, nicknames []string) []string {
	slices.Sort(nicknames)
	nicknames = slices.Compact(nicknames)
	inTerm := trigrams(term)
	distances := make(map[string]float64, len(nicknames))
	for _, nickname := range nicknames {
		// The trigram distance is the share of the trigrams that only one string has,
		// or 1 when neither has any, as in pg_trgm
		shared, union := sharedTrigrams(trigrams(nickname), inTerm)
		if union == 0 {
			union = 1
		}
		edits, longer := levenshtein(nickname, term), max(len(nickname), len(term), 1)
		// One division of whole numbers, so equal distances come out as equal floats
		distances[nickname] = float64(2*(union-shared)*longer+edits*union) / float64(union*longer)
	}
	// Stable, so equally close nicknames stay in alphabetical order
	slices.SortStableFunc(nicknames, func(a, b string) int { return cmp.Compare(distances[a], distances[b]) })
	return nicknames[:min(len(nicknames), maxSearchResults)]
}

// sharedTrigrams counts the trigrams both sets hold and the trigrams either holds
func sharedTrigrams(a, b map[string]bool) (shared, union int) {
	for trigram := range a {
		if b[trigram] {
			shared++
		}
	}
	return shared, len(a) + len(b) - shared
}

// trigrams returns the three-byte pieces of each word in s, as pg_trgm cuts an ASCII string:
// a word is a run of letters and digits, padded with two spaces before and one after
func trigrams(s string) map[string]bool {
	pieces := map[string]bool{}
	for _, word := range strings.FieldsFunc(s, isSeparator) {
		padded := "  " + word + " "
		for i := 0; i+3 <= len(padded); i++ {
			pieces[padded[i:i+3]] = true
		}
	}
	return pieces
}

func isSeparator(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }

// levenshtein counts the fewest single-byte inserts, deletes and replacements that turn a into b
func levenshtein(a, b string) int {
	// After i rounds, row[j] is the distance between a[:i] and b[:j]
	row := make([]int, len(b)+1)
	for j := range row {
		row[j] = j
	}
	for i := 1; i <= len(a); i++ {
		diagonal := row[0]
		row[0] = i
		for j := 1; j <= len(b); j++ {
			replaced := diagonal
			if a[i-1] != b[j-1] {
				replaced++
			}
			diagonal = row[j]
			row[j] = min(row[j]+1, row[j-1]+1, replaced)
		}
	}
	return row[len(b)]
}
