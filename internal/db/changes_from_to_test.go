package db

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/bcmk/siren/v2/lib/cmdlib"
)

func streamerID(t *testing.T, d *Database, nickname string) int {
	t.Helper()
	s := d.MaybeStreamer(nickname)
	if s == nil {
		t.Fatalf("streamer %s not found", nickname)
	}
	return s.ID
}

type statusInsert struct {
	nickname string
	status   cmdlib.StatusKind
	ts       int
}

type changesTestCase struct {
	name      string
	inserts   []statusInsert
	streamers []string // nicknames to query, or empty for unknown ID test
	from      int
	to        int
	// expected changes per streamer nickname;
	// use "" key for unknown streamer ID tests
	expected map[string][]StatusChange
}

var changesTests = []changesTestCase{
	{
		name:      "no changes in range",
		inserts:   []statusInsert{{"a", cmdlib.StatusOnline, 100}},
		streamers: []string{"a"},
		from:      200,
		to:        300,
		expected: map[string][]StatusChange{
			"a": {
				{Status: cmdlib.StatusOnline, Timestamp: 100},
				{Timestamp: 300},
			},
		},
	},
	{
		name: "all in range",
		inserts: []statusInsert{
			{"a", cmdlib.StatusOnline, 100},
			{"a", cmdlib.StatusOffline, 200},
		},
		streamers: []string{"a"},
		from:      50,
		to:        300,
		expected: map[string][]StatusChange{
			"a": {
				{Status: cmdlib.StatusOnline, Timestamp: 100},
				{Status: cmdlib.StatusOffline, Timestamp: 200},
				{Timestamp: 300},
			},
		},
	},
	{
		name: "before range with in-range changes",
		inserts: []statusInsert{
			{"a", cmdlib.StatusOnline, 100},
			{"a", cmdlib.StatusOffline, 200},
			{"a", cmdlib.StatusOnline, 300},
		},
		streamers: []string{"a"},
		from:      250,
		to:        400,
		expected: map[string][]StatusChange{
			"a": {
				{Status: cmdlib.StatusOffline, Timestamp: 200},
				{Status: cmdlib.StatusOnline, Timestamp: 300},
				{Timestamp: 400},
			},
		},
	},
	{
		name: "multiple streamers",
		inserts: []statusInsert{
			{"a", cmdlib.StatusOnline, 100},
			{"b", cmdlib.StatusOffline, 100},
			{"a", cmdlib.StatusOffline, 200},
			{"b", cmdlib.StatusOnline, 200},
		},
		streamers: []string{"a", "b"},
		from:      150,
		to:        300,
		expected: map[string][]StatusChange{
			"a": {
				{Status: cmdlib.StatusOnline, Timestamp: 100},
				{Status: cmdlib.StatusOffline, Timestamp: 200},
				{Timestamp: 300},
			},
			"b": {
				{Status: cmdlib.StatusOffline, Timestamp: 100},
				{Status: cmdlib.StatusOnline, Timestamp: 200},
				{Timestamp: 300},
			},
		},
	},
	{
		name: "all before range",
		inserts: []statusInsert{
			{"a", cmdlib.StatusOnline, 100},
			{"a", cmdlib.StatusOffline, 200},
		},
		streamers: []string{"a"},
		from:      300,
		to:        400,
		expected: map[string][]StatusChange{
			"a": {
				{Status: cmdlib.StatusOffline, Timestamp: 200},
				{Timestamp: 400},
			},
		},
	},
	{
		name: "exact boundary",
		inserts: []statusInsert{
			{"a", cmdlib.StatusOnline, 100},
			{"a", cmdlib.StatusOffline, 200},
		},
		streamers: []string{"a"},
		from:      200,
		to:        300,
		expected: map[string][]StatusChange{
			"a": {
				{Status: cmdlib.StatusOnline, Timestamp: 100},
				{Status: cmdlib.StatusOffline, Timestamp: 200},
				{Timestamp: 300},
			},
		},
	},
}

func TestChangesFromToForStreamers(t *testing.T) {
	for _, tc := range changesTests {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			defer db.terminate()

			for _, ins := range tc.inserts {
				db.UpsertUnconfirmedStatusChanges(
					[]StatusChange{{Nickname: ins.nickname, Status: ins.status}},
					ins.ts,
				)
			}

			ids := make([]int, len(tc.streamers))
			idToNickname := make(map[int]string)
			for i, nick := range tc.streamers {
				ids[i] = streamerID(t, db.Database, nick)
				idToNickname[ids[i]] = nick
			}

			result := db.ChangesFromToForStreamers(ids, tc.from, tc.to)

			expected := make(map[int][]StatusChange)
			for nick, changes := range tc.expected {
				expected[streamerID(t, db.Database, nick)] = changes
			}

			if !reflect.DeepEqual(result, expected) {
				t.Errorf("expected %v, got %v", expected, result)
			}
		})
	}
}

func TestChangesFromToForStreamersEmpty(t *testing.T) {
	db := newTestDB(t)
	defer db.terminate()

	result := db.ChangesFromToForStreamers([]int{}, 100, 200)
	if len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}
}

func TestChangesFromToForStreamersUnknownID(t *testing.T) {
	db := newTestDB(t)
	defer db.terminate()

	result := db.ChangesFromToForStreamers([]int{999999}, 100, 200)
	expected := map[int][]StatusChange{
		999999: {{Timestamp: 200}},
	}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("expected %v, got %v", expected, result)
	}
}

func TestChangesFromToForStreamersConsistency(t *testing.T) {
	db := newTestDB(t)
	defer db.terminate()

	// Insert many changes, verify result is always sorted
	for i := range 20 {
		status := cmdlib.StatusOffline
		if i%2 == 0 {
			status = cmdlib.StatusOnline
		}
		db.UpsertUnconfirmedStatusChanges(
			[]StatusChange{{Nickname: "a", Status: status}},
			100+i*10,
		)
	}

	id := streamerID(t, db.Database, "a")
	result := db.ChangesFromToForStreamers([]int{id}, 150, 350)
	changes := result[id]

	for i := 1; i < len(changes); i++ {
		if changes[i].Timestamp < changes[i-1].Timestamp {
			t.Errorf(
				"not sorted at index %d: %d < %d",
				i,
				changes[i].Timestamp,
				changes[i-1].Timestamp,
			)
		}
	}

	last := changes[len(changes)-1]
	if last.Timestamp != 350 {
		t.Errorf(
			"last entry should be sentinel with timestamp 350, got %v",
			last,
		)
	}

	first := changes[0]
	if first.Timestamp >= 150 {
		t.Errorf(
			"first entry should be before-range, got timestamp %d",
			first.Timestamp,
		)
	}

	for i, c := range changes[:len(changes)-1] {
		if c.Status != cmdlib.StatusOnline && c.Status != cmdlib.StatusOffline {
			t.Errorf(
				"unexpected status at index %d: %v",
				i,
				c.Status,
			)
		}
	}

	fmt.Printf(
		"consistency check: %d changes returned for range [150, 350]\n",
		len(changes),
	)
}
