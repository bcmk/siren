package main

import (
	"reflect"
	"testing"

	"github.com/bcmk/siren/v6/internal/db"
	"github.com/bcmk/siren/v6/lib/cmdlib"
)

func TestOnlineCells(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		changes     map[int][]db.StatusChange
		from        int
		to          int
		cellSeconds int
		want        map[int][]bool
	}{
		{
			name: "online period",
			changes: map[int][]db.StatusChange{1: {
				{Status: cmdlib.StatusUnknown, Timestamp: 100},
				{Status: cmdlib.StatusOffline, Timestamp: 100},
				{Status: cmdlib.StatusOnline, Timestamp: 110},
				{Status: cmdlib.StatusOffline, Timestamp: 130},
				{Timestamp: 150},
			}},
			from:        100,
			to:          150,
			cellSeconds: 10,
			want:        map[int][]bool{1: {false, true, true, false, false}},
		},
		{
			name:        "online before range no changes",
			changes:     map[int][]db.StatusChange{1: {{Status: cmdlib.StatusOnline, Timestamp: 200}, {Timestamp: 250}}},
			from:        200,
			to:          250,
			cellSeconds: 10,
			want:        map[int][]bool{1: {true, true, true, true, true}},
		},
		{
			name:        "offline before range no changes",
			changes:     map[int][]db.StatusChange{1: {{Status: cmdlib.StatusOffline, Timestamp: 200}, {Timestamp: 250}}},
			from:        200,
			to:          250,
			cellSeconds: 10,
			want:        map[int][]bool{1: {false, false, false, false, false}},
		},
		{
			name: "before range online then offline in range",
			changes: map[int][]db.StatusChange{1: {
				{Status: cmdlib.StatusOnline, Timestamp: 200},
				{Status: cmdlib.StatusOffline, Timestamp: 220},
				{Timestamp: 250},
			}},
			from:        200,
			to:          250,
			cellSeconds: 10,
			want:        map[int][]bool{1: {true, true, false, false, false}},
		},
		{
			name: "multiple streamers",
			changes: map[int][]db.StatusChange{
				1: {
					{Status: cmdlib.StatusUnknown, Timestamp: 100},
					{Status: cmdlib.StatusOnline, Timestamp: 110},
					{Status: cmdlib.StatusOffline, Timestamp: 120},
					{Timestamp: 150},
				},
				2: {
					{Status: cmdlib.StatusUnknown, Timestamp: 100},
					{Status: cmdlib.StatusOffline, Timestamp: 110},
					{Status: cmdlib.StatusOnline, Timestamp: 120},
					{Timestamp: 150},
				},
			},
			from:        100,
			to:          150,
			cellSeconds: 10,
			want: map[int][]bool{
				1: {false, true, false, false, false},
				2: {false, false, true, true, true},
			},
		},
		{
			name:        "unknown before range",
			changes:     map[int][]db.StatusChange{1: {{Status: cmdlib.StatusUnknown, Timestamp: 200}, {Timestamp: 250}}},
			from:        200,
			to:          250,
			cellSeconds: 10,
			want:        map[int][]bool{1: {false, false, false, false, false}},
		},
		{
			name: "online to unknown in range",
			changes: map[int][]db.StatusChange{1: {
				{Status: cmdlib.StatusOnline, Timestamp: 200},
				{Status: cmdlib.StatusUnknown, Timestamp: 220},
				{Timestamp: 250},
			}},
			from:        200,
			to:          250,
			cellSeconds: 10,
			want:        map[int][]bool{1: {true, true, false, false, false}},
		},
		{
			name: "unknown to online in range",
			changes: map[int][]db.StatusChange{1: {
				{Status: cmdlib.StatusUnknown, Timestamp: 200},
				{Status: cmdlib.StatusOnline, Timestamp: 220},
				{Timestamp: 250},
			}},
			from:        200,
			to:          250,
			cellSeconds: 10,
			want:        map[int][]bool{1: {false, false, true, true, true}},
		},
		{
			name:        "no streamers",
			changes:     map[int][]db.StatusChange{},
			from:        100,
			to:          200,
			cellSeconds: 10,
			want:        map[int][]bool{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := onlineCells(tc.changes, tc.from, tc.to, tc.cellSeconds)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("cells = %v, want %v", got, tc.want)
			}
		})
	}
}
