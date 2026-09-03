package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bcmk/siren/v5/internal/db"
)

// monthTestRows is a grid of two full weeks and a started third, all cells offline.
func monthTestRows() []tplData {
	return []tplData{
		{"day": 27, "month": 7, "cells": make([]bool, monthRowCells)},
		{"day": 3, "month": 8, "cells": make([]bool, monthRowCells)},
		{"day": 10, "month": 8, "cells": make([]bool, 3)},
	}
}

// TestMonthChunkSeparatesRows pins the fragment mirroring week_chunk:
// it dispatches each row through the month template and puts a blank line between rows.
func TestMonthChunkSeparatesRows(t *testing.T) {
	t.Parallel()
	for _, lang := range realLangs {
		t.Run(lang, func(t *testing.T) {
			t.Parallel()
			tpl := realTemplates(t, lang)
			chunk := func(rows ...tplData) string {
				params := &renderParams{templates: tpl, key: "month_chunk", data: tplData{"rows": rows}}
				return params.render("")
			}
			first := tplData{
				"rows":          monthTestRows(),
				"header":        monthHeader(time.Monday),
				"timezone":      "UTC",
				"streamer_link": "alica_webcam",
			}
			second := tplData{
				"rows":          monthTestRows(),
				"header":        monthHeader(time.Sunday),
				"timezone":      "UTC",
				"streamer_link": "bob_cam",
			}

			one := chunk(first)
			if !strings.Contains(one, "alica_webcam") {
				t.Fatalf("a row does not render through the month template: %q", one)
			}
			if got, want := chunk(first, second), one+"\n\n"+chunk(second); got != want {
				t.Errorf("two rows = %q, want %q", got, want)
			}
			if got := chunk(); got != "" {
				t.Errorf("no rows = %q, want empty", got)
			}
		})
	}
}

// TestMonthTemplateGrid pins the layout to the character:
// a header of weekday names from the chat's week start, a date opening each week row,
// two half-day cells per day sitting under the day's name.
// English alone: the ru template shares the layout and differs only in its words.
func TestMonthTemplateGrid(t *testing.T) {
	t.Parallel()
	rows := monthTestRows()
	rows[0]["cells"].([]bool)[0] = true
	rows[2]["cells"].([]bool)[2] = true
	params := &renderParams{templates: realTemplates(t, "en"), key: "month", data: tplData{
		"rows":          rows,
		"header":        monthHeader(time.Monday),
		"timezone":      "UTC",
		"streamer_link": "alica_webcam",
	}}
	want := "alica_webcam's month (UTC):\n\n<code>" +
		"       Mo Tu We Th Fr Sa Su\n" +
		"27 Jul #- -- -- -- -- -- --\n" +
		" 3 Aug -- -- -- -- -- -- --\n" +
		"10 Aug -- #</code>"
	if got := params.render(""); got != want {
		t.Errorf("rendered grid = %q, want %q", got, want)
	}
}

// TestMonthTemplateEmptyGrid covers a streamer the database has never seen:
// no rows, yet the weekday header still shows, as the week template's ruler line does.
func TestMonthTemplateEmptyGrid(t *testing.T) {
	t.Parallel()
	headers := map[string]string{"en": "Mo Tu We Th Fr Sa Su", "ru": "Пн Вт Ср Чт Пт Сб Вс"}
	for _, lang := range realLangs {
		t.Run(lang, func(t *testing.T) {
			t.Parallel()
			params := &renderParams{templates: realTemplates(t, lang), key: "month", data: tplData{
				"rows":          []tplData(nil),
				"header":        monthHeader(time.Monday),
				"timezone":      "UTC",
				"streamer_link": "alica_webcam",
			}}
			got := params.render("")
			if !strings.Contains(got, "alica_webcam") {
				t.Errorf("empty grid rendered %q", got)
			}
			if !strings.Contains(got, headers[lang]) {
				t.Errorf("empty grid misses the weekday header: %q", got)
			}
		})
	}
}

// TestMonthRows ties the slicing to the window: week rows of monthRowCells,
// each labelled with the date its first cell opens, walked over month and year bounds.
func TestMonthRows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		cells int
		date  time.Time
		rows  []int
		days  [][2]int
	}{
		{
			"across a month bound", 30,
			time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
			[]int{14, 14, 2},
			[][2]int{{27, 7}, {3, 8}, {10, 8}},
		},
		{
			"across a year bound", 30,
			time.Date(2026, 12, 21, 0, 0, 0, 0, time.UTC),
			[]int{14, 14, 2},
			[][2]int{{21, 12}, {28, 12}, {4, 1}},
		},
		{"no cells", 0, time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC), nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rows := monthRows(make([]bool, tc.cells), tc.date)
			if len(rows) != len(tc.rows) {
				t.Fatalf("rows = %d, want %d", len(rows), len(tc.rows))
			}
			for i, row := range rows {
				if got := len(row["cells"].([]bool)); got != tc.rows[i] {
					t.Errorf("row %d holds %d cells, want %d", i, got, tc.rows[i])
				}
				if day, month := row["day"], row["month"]; day != tc.days[i][0] || month != tc.days[i][1] {
					t.Errorf("row %d opens %v.%v, want %02d.%02d", i, day, month, tc.days[i][0], tc.days[i][1])
				}
			}
		})
	}
}

// The never-online branch feeds streamerListEntry rows to month_never_online;
// a subscription with no online half-days must come back in that reply, not vanish.
// The real templates render, tying the call site's data keys to the template's ranges,
// which a stub that ignores its data cannot.
func TestShowMonthNeverOnline(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()
	w.tpl["test"] = realTemplates(t, "en")
	m := testMessage(w, 10, "month", 100)
	id := insertTestStreamer(&w.db, db.Streamer{Nickname: "ghost_model"})
	w.db.AddSubscription(m.userID, id, "test")
	w.showMonth(m, "")
	if n := w.sendQueue.Len(); n != 1 {
		t.Fatalf("queued replies = %d, want 1", n)
	}
	never := w.sendQueue.pop()
	never.message.render("")
	if got := never.message.(*messageParams).Text; !strings.Contains(got, "ghost_model") {
		t.Errorf("the never-online reply lost the streamer: %q", got)
	}
}
