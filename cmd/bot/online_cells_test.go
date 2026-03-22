package main

import (
	"reflect"
	"testing"

	"github.com/bcmk/siren/v2/internal/db"
	"github.com/bcmk/siren/v2/lib/cmdlib"
)

type cellsInsert struct {
	nickname string
	status   cmdlib.StatusKind
	ts       int
}

type cellsTestCase struct {
	name        string
	inserts     []cellsInsert
	streamers   []string
	from        int
	to          int
	cellSeconds int
	// expected cells per streamer nickname
	expected map[string][]bool
}

var cellsTests = []cellsTestCase{
	{
		name: "online period",
		inserts: []cellsInsert{
			{"a", cmdlib.StatusOffline, 100},
			{"a", cmdlib.StatusOnline, 110},
			{"a", cmdlib.StatusOffline, 130},
		},
		streamers:   []string{"a"},
		from:        100,
		to:          150,
		cellSeconds: 10,
		expected: map[string][]bool{
			"a": {false, true, true, false, false},
		},
	},
	{
		name:        "online before range no changes",
		inserts:     []cellsInsert{{"a", cmdlib.StatusOnline, 100}},
		streamers:   []string{"a"},
		from:        200,
		to:          250,
		cellSeconds: 10,
		expected: map[string][]bool{
			"a": {true, true, true, true, true},
		},
	},
	{
		name:        "offline before range no changes",
		inserts:     []cellsInsert{{"a", cmdlib.StatusOffline, 100}},
		streamers:   []string{"a"},
		from:        200,
		to:          250,
		cellSeconds: 10,
		expected: map[string][]bool{
			"a": {false, false, false, false, false},
		},
	},
	{
		name: "before range online then offline in range",
		inserts: []cellsInsert{
			{"a", cmdlib.StatusOnline, 100},
			{"a", cmdlib.StatusOffline, 220},
		},
		streamers:   []string{"a"},
		from:        200,
		to:          250,
		cellSeconds: 10,
		expected: map[string][]bool{
			"a": {true, true, false, false, false},
		},
	},
	{
		name: "multiple streamers",
		inserts: []cellsInsert{
			{"a", cmdlib.StatusOnline, 110},
			{"b", cmdlib.StatusOffline, 110},
			{"a", cmdlib.StatusOffline, 120},
			{"b", cmdlib.StatusOnline, 120},
		},
		streamers:   []string{"a", "b"},
		from:        100,
		to:          150,
		cellSeconds: 10,
		expected: map[string][]bool{
			"a": {false, true, false, false, false},
			"b": {false, false, true, true, true},
		},
	},
	{
		name: "unknown before range",
		inserts: []cellsInsert{
			{"a", cmdlib.StatusUnknown, 100},
		},
		streamers:   []string{"a"},
		from:        200,
		to:          250,
		cellSeconds: 10,
		expected: map[string][]bool{
			"a": {false, false, false, false, false},
		},
	},
	{
		name: "online to unknown in range",
		inserts: []cellsInsert{
			{"a", cmdlib.StatusOnline, 100},
			{"a", cmdlib.StatusUnknown, 220},
		},
		streamers:   []string{"a"},
		from:        200,
		to:          250,
		cellSeconds: 10,
		expected: map[string][]bool{
			"a": {true, true, false, false, false},
		},
	},
	{
		name: "unknown to online in range",
		inserts: []cellsInsert{
			{"a", cmdlib.StatusUnknown, 100},
			{"a", cmdlib.StatusOnline, 220},
		},
		streamers:   []string{"a"},
		from:        200,
		to:          250,
		cellSeconds: 10,
		expected: map[string][]bool{
			"a": {false, false, true, true, true},
		},
	},
}

func TestOnlineCells(t *testing.T) {
	for _, tc := range cellsTests {
		t.Run(tc.name, func(t *testing.T) {
			cmdlib.Verbosity = cmdlib.SilentVerbosity
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase(make(chan bool, 1))

			for _, ins := range tc.inserts {
				w.db.UpsertUnconfirmedStatusChanges(
					[]db.StatusChange{{Nickname: ins.nickname, Status: ins.status}},
					ins.ts,
				)
			}

			ids := make([]int, len(tc.streamers))
			nickToID := make(map[string]int)
			for i, nick := range tc.streamers {
				s := w.db.MaybeStreamer(nick)
				if s == nil {
					t.Fatalf("streamer %s not found", nick)
				}
				ids[i] = s.ID
				nickToID[nick] = s.ID
			}

			changes := w.db.ChangesFromToForStreamers(ids, tc.from, tc.to)
			result := onlineCells(changes, tc.from, tc.to, tc.cellSeconds)

			for nick, exp := range tc.expected {
				id := nickToID[nick]
				if !reflect.DeepEqual(result[id], exp) {
					t.Errorf(
						"streamer %s: expected %v, got %v",
						nick,
						exp,
						result[id],
					)
				}
			}
		})
	}
}

func TestOnlineCellsEmpty(t *testing.T) {
	cmdlib.Verbosity = cmdlib.SilentVerbosity
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))

	changes := w.db.ChangesFromToForStreamers([]int{}, 100, 200)
	result := onlineCells(changes, 100, 200, 10)
	if len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}
}
