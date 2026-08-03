package main

import (
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/bcmk/siren/v3/lib/cmdlib"
)

// TestParseTimezone pins the grammar /timezone accepts.
// The server settles the spelling, so a typist's casing no longer decides what is stored
// and the answers no longer turn on the host the bot runs on.
func TestParseTimezone(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initTimezones()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"canonical", "Europe/Berlin", "Europe/Berlin"},
		{"lowercase", "europe/berlin", "Europe/Berlin"},
		{"underscored word", "america/new_york", "America/New_York"},
		{"lowercase utc", "utc", "UTC"},
		{"surrounding spaces", "  Europe/Berlin  ", "Europe/Berlin"},
		// The families a recasing rule could not reach, whatever the host.
		// A legacy alias, US/Eastern, would belong here but for the server deciding
		// whether it ships the backward-compatibility links at all.
		{"fixed offset zone", "etc/gmt+3", "Etc/GMT+3"},
		{"lowercase particle", "europe/isle_of_man", "Europe/Isle_of_Man"},
		{"inner capital", "antarctica/mcmurdo", "Antarctica/McMurdo"},
		{"hyphenated word", "america/port-au-prince", "America/Port-au-Prince"},
		// Neither names a chat's zone: Local is the host's, Factory a placeholder.
		{"local", "Local", ""},
		{"factory", "Factory", ""},
		{"empty", "", ""},
		{"unknown zone", "Europe/Atlantis", ""},
		{"offset", "+3", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			loc, zone, ok := w.parseTimezone(tc.in)
			if ok != (tc.want != "") {
				t.Fatalf("parseTimezone(%q) ok = %v, want %v", tc.in, ok, tc.want != "")
			}
			if zone != tc.want {
				t.Errorf("parseTimezone(%q) zone = %q, want %q", tc.in, zone, tc.want)
			}
			if ok && loc.String() != tc.want {
				t.Errorf("parseTimezone(%q) location = %q, want %q", tc.in, loc, tc.want)
			}
		})
	}
}

// TestParseTimezoneAgreesWithTheServer walks every name the server holds through the resolver.
// The two carry their own copies of the zone database and drift as either is upgraded,
// so the gap is measured here rather than assumed away.
func TestParseTimezoneAgreesWithTheServer(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initTimezones()

	// setZoneNames drops what it cannot load, so the drift shows as a name the server
	// offered and the map does not hold, rather than as a failure at resolution.
	var unloadable []string
	for lower, canonical := range w.db.TimezoneNames() {
		loc, zone, ok := w.parseTimezone(lower)
		if !ok {
			unloadable = append(unloadable, canonical)
			continue
		}
		if zone != canonical || loc.String() != canonical {
			t.Errorf("%q resolved to %q / %q, want %q", lower, zone, loc, canonical)
		}
	}
	if len(unloadable) != 0 {
		t.Errorf("the server holds %d zones this binary cannot load: %q", len(unloadable), unloadable)
	}
}

// TestWeekWindow pins the grid to the chat's own midnight, not UTC's:
// the same instant starts a different day either side of the date line.
// The DST cases are the ones a zone can break:
// a midnight the shift skips, and a fall-back week that holds more hours than the grid has cells.
func TestWeekWindow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		zone    string
		now     string
		start   string
		weekday time.Weekday
		cells   int
	}{
		{"utc", "UTC", "2026-08-02T23:30:00Z", "2026-07-27T00:00:00Z", time.Monday, 168},
		{
			"ahead of utc", "Europe/Berlin", "2026-08-02T23:30:00Z",
			"2026-07-28T00:00:00+02:00", time.Tuesday, 146,
		},
		{
			"behind utc", "America/New_York", "2026-08-02T23:30:00Z",
			"2026-07-27T00:00:00-04:00", time.Monday, 164,
		},
		// A spring-forward two days back leaves the start a true local midnight, in the prior offset.
		{
			"across a spring forward", "Europe/Berlin", "2026-03-31T10:00:00Z",
			"2026-03-25T00:00:00+01:00", time.Wednesday, 155,
		},
		// Havana turns its clocks at midnight, so the day the grid opens on has no 00:00 at all.
		{
			"midnight the shift skips", "America/Havana", "2026-03-08T17:00:00Z",
			"2026-03-02T00:00:00-05:00", time.Monday, 156,
		},
		{
			"the skipped midnight itself", "America/Santiago", "2026-09-12T15:00:00Z",
			"2026-09-06T01:00:00-03:00", time.Sunday, 155,
		},
		// A fall-back week runs 169 hours, so the grid opens an hour past midnight to hold 168.
		{
			"across a fall back", "Europe/Berlin", "2026-10-31T22:30:00Z",
			"2026-10-25T01:00:00+02:00", time.Sunday, 168,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatalf("cannot load %s: %v", tc.zone, err)
			}
			now, err := time.Parse(time.RFC3339, tc.now)
			if err != nil {
				t.Fatalf("cannot parse %s: %v", tc.now, err)
			}
			from, to, weekday := weekWindow(now, loc)
			if got := time.Unix(int64(from), 0).In(loc).Format(time.RFC3339); got != tc.start {
				t.Errorf("start = %s, want %s", got, tc.start)
			}
			if weekday != tc.weekday {
				t.Errorf("weekday = %s, want %s", weekday, tc.weekday)
			}
			// The count the template chunks into rows, which must never reach an eighth.
			if cells := (to - from + 3599) / 3600; cells != tc.cells {
				t.Errorf("cells = %d, want %d", cells, tc.cells)
			}
		})
	}
}

// TestTimezoneCommandGates drives /timezone through the dispatch rather than the handler,
// so the gate it carries is the one under test: a group asks its admins.
// A chat's zone is its own, not the grid's, so nothing here turns on enable_week.
func TestTimezoneCommandGates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		command    string
		chatID     int64
		member     *models.ChatMember
		want       string
		wantStored bool
		wantLookup bool
	}{
		{"private chat sets it", "timezone", 10, nil, "Timezone Europe/Berlin", true, false},
		{
			"group admin sets it", "timezone", -10,
			&models.ChatMember{Type: models.ChatMemberTypeAdministrator},
			"Timezone Europe/Berlin", true, true,
		},
		{
			"group member is refused", "timezone", -10,
			&models.ChatMember{Type: models.ChatMemberTypeMember}, "AdminsOnly", false, true,
		},
		// Each reset case starts with a zone stored,
		// so a refusal is visible as that zone surviving rather than as an absent one.
		{"private chat resets it", "reset_timezone", 10, nil, "Timezone UTC", false, false},
		{
			"group member cannot reset", "reset_timezone", -10,
			&models.ChatMember{Type: models.ChatMemberTypeMember}, "AdminsOnly", true, true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()
			w.initCache()
			w.initTimezones()
			looked := false
			w.chatMember = func(string, int64, int64) (*models.ChatMember, error) {
				looked = true
				return tc.member, nil
			}

			m := testMessage(w, tc.chatID, tc.command, 100)
			arg := "Europe/Berlin"
			if tc.command == "reset_timezone" {
				// Something to clear, so a refused reset shows up as the zone surviving.
				arg = ""
				w.db.SetTimezone(m.userID, "Europe/Berlin")
			}
			snd := sender{from: &models.User{ID: 99}}
			w.processIncomingCommand(m, snd, tc.command, arg, false)

			if n := w.sendQueue.Len(); n != 1 {
				t.Fatalf("queued replies = %d, want 1", n)
			}
			queued := w.sendQueue.pop()
			queued.message.render("")
			if got := queued.message.(*messageParams).Text; got != tc.want {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
			stored := w.db.MustInt(
				"select count(*) from users where chat_id = $1 and timezone = 'Europe/Berlin'",
				tc.chatID)
			if (stored == 1) != tc.wantStored {
				t.Errorf("zone stored = %v, want %v", stored == 1, tc.wantStored)
			}
			// A refused command must not have cost a round trip to Telegram to refuse.
			if looked != tc.wantLookup {
				t.Errorf("member lookup made = %v, want %v", looked, tc.wantLookup)
			}
			// A cleared column has to read as unset, not as the empty string:
			// an empty one is not null, so the settings listing would offer to reset nothing.
			if tc.command == "reset_timezone" && !tc.wantStored {
				cleared := w.db.MustInt(
					"select count(*) from users where chat_id = $1 and timezone is null", tc.chatID)
				if cleared != 1 {
					t.Error("the reset left the column set rather than null")
				}
			}
		})
	}
}

// TestChatLocationKeepsAZoneTheServerDropped: the server's list says what a chat may pick,
// not what it may keep. A packaging change can withdraw a name the binary still holds,
// and a chat that named it meant it, so the row survives and is honoured.
func TestChatLocationKeepsAZoneTheServerDropped(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()
	w.initTimezones()
	m := testMessage(w, 10, "timezone", 100)
	// Loadable here, and withdrawn from the offered list as a rebuilt server would withdraw it.
	const dropped = "Europe/Berlin"
	w.db.SetTimezone(m.userID, dropped)
	delete(w.zoneNames, "europe/berlin")

	loc, zone := w.chatLocation(w.mustUserByID(m.userID))
	if zone != dropped || loc.String() != dropped {
		t.Errorf("read as %q / %q, want %q", zone, loc, dropped)
	}
	kept := w.db.MustInt(
		"select count(*) from users where id = $1 and timezone = $2", int64(m.userID), dropped)
	if kept != 1 {
		t.Error("a zone the binary can still load was overwritten")
	}
}

// TestChatLocationKeepsADeadZone: a name neither the server nor this binary can resolve
// is reported and fallen back from, and left in the column.
// What resolves turns on the tzdata the binary carries,
// so a listing must not destroy a choice a rollback would make good again.
func TestChatLocationKeepsADeadZone(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()
	w.initTimezones()
	m := testMessage(w, 10, "timezone", 100)
	const dead = "Europe/Atlantis"
	w.db.MustExec("update users set timezone = $1 where id = $2", dead, int64(m.userID))

	if _, zone := w.chatLocation(w.mustUserByID(m.userID)); zone != utcZone {
		t.Errorf("a dead zone read as %q, want %q", zone, utcZone)
	}
	kept := w.db.MustInt(
		"select count(*) from users where id = $1 and timezone = $2", int64(m.userID), dead)
	if kept != 1 {
		t.Error("a listing overwrote a zone the chat chose")
	}
}

// TestSettingsReportsTheZone drives the real settings through the real template,
// so what is under test is the keys the call site passes, not a literal restating them.
// A key renamed there reads as absent, an absent key is false in an if,
// and a false if renders nothing at all — which no fixture built by hand can notice.
func TestSettingsReportsTheZone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		zone      string
		wantZone  string
		wantReset bool
	}{
		{"a chat that set one", "Europe/Berlin", "Europe/Berlin", true},
		{"a chat that set none", "", utcZone, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()
			w.initCache()
			w.initTimezones()
			// The real templates and a Settings translation naming the real key,
			// so the render is the one a chat receives.
			w.tpl["test"] = realTemplates(t, "en")
			tr := testTranslations
			tr.Settings = &cmdlib.Translation{Key: "settings", Parse: cmdlib.ParseHTML}
			w.tr["test"] = &tr

			m := testMessage(w, 10, "settings", 100)
			if tc.zone != "" {
				w.db.SetTimezone(m.userID, tc.zone)
			}
			w.settings(m)

			queued := w.sendQueue.pop()
			queued.message.render("")
			got := queued.message.(*messageParams).Text
			if !strings.Contains(got, "Timezone: <b>"+tc.wantZone+"</b>") {
				t.Errorf("the listing does not report %q: %q", tc.wantZone, got)
			}
			if strings.Contains(got, "reset_timezone") != tc.wantReset {
				t.Errorf("reset offered = %v, want %v: %q",
					!tc.wantReset, tc.wantReset, got)
			}
		})
	}
}

// settingsData is what worker.settings hands the template.
func settingsData(timezoneSet bool) tplData {
	return tplData{
		"subscriptions_used":              1,
		"total_subscriptions":             3,
		"show_images":                     true,
		"offline_notifications_supported": true,
		"offline_notifications":           true,
		"subject_supported":               true,
		"show_subject":                    true,
		"silent_messages":                 false,
		"in_group":                        false,
		"member_subscriptions":            false,
		"timezone":                        "Europe/Berlin",
		"timezone_set":                    timezoneSet,
		"can_manage_affiliate":            false,
		"affiliate_params":                nil,
	}
}

// TestTimezoneTemplatesRender ties the call sites' data keys to the real templates,
// which a stub that ignores its data cannot.
func TestTimezoneTemplatesRender(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		key     string
		data    tplData
		want    string
		wantOut bool
	}{
		{"status", "timezone", tplData{"timezone": "Europe/Berlin"}, "Europe/Berlin", true},
		// The zone list link stands for the help itself, being what every language shares.
		{
			"status asked for", "timezone",
			tplData{"timezone": "Europe/Berlin", "help": true}, "List_of_tz_database_time_zones", true,
		},
		{
			"status after a change", "timezone",
			tplData{"timezone": "Europe/Berlin", "help": false}, "List_of_tz_database_time_zones", false,
		},
		{"invalid", "timezone_invalid", nil, "", true},
		{
			"week header", "week",
			tplData{
				"hours":         make([]bool, 24),
				"weekday":       0,
				"timezone":      "Europe/Berlin",
				"streamer_link": "alica_webcam",
			},
			"Europe/Berlin", true,
		},
		{"settings", "settings", settingsData(true), "Europe/Berlin", true},
		// The reset is offered only where there is something to reset,
		// so the key the call site passes is tied to the branch the template takes.
		{"settings with a zone set", "settings", settingsData(true), "reset_timezone", true},
		{"settings with none set", "settings", settingsData(false), "reset_timezone", false},
	}
	for _, lang := range realLangs {
		for _, tc := range tests {
			t.Run(lang+" "+tc.name, func(t *testing.T) {
				t.Parallel()
				params := &renderParams{templates: realTemplates(t, lang), key: tc.key, data: tc.data}
				out := params.render("")
				if out == "" {
					t.Fatalf("%s rendered empty", tc.key)
				}
				if strings.Contains(out, "<no value>") {
					t.Errorf("%s rendered a missing field: %q", tc.key, out)
				}
				if tc.want != "" && strings.Contains(out, tc.want) != tc.wantOut {
					t.Errorf("%s contains %q = %v, want %v: %q",
						tc.key, tc.want, !tc.wantOut, tc.wantOut, out)
				}
			})
		}
	}
}

// TestSetTimezone walks the command: a chat starts on UTC, keeps what it sets,
// and keeps it through a rejected one.
func TestSetTimezone(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()
	w.initTimezones()
	m := testMessage(w, 10, "timezone", 100)

	reply := func() string {
		queued := w.sendQueue.pop()
		queued.message.render("")
		return queued.message.(*messageParams).Text
	}

	if _, zone := w.chatLocation(w.mustUserByID(m.userID)); zone != utcZone {
		t.Errorf("a chat that set nothing reads as %q, want %q", zone, utcZone)
	}

	w.setTimezone(m, "europe/berlin")
	if got := reply(); !strings.Contains(got, "Europe/Berlin") {
		t.Errorf("the set reply misses the zone: %q", got)
	}
	if _, zone := w.chatLocation(w.mustUserByID(m.userID)); zone != "Europe/Berlin" {
		t.Errorf("stored zone = %q, want Europe/Berlin", zone)
	}

	w.setTimezone(m, "Europe/Atlantis")
	if got := reply(); got != "TimezoneInvalid" {
		t.Errorf("an unknown zone answered %q, want TimezoneInvalid", got)
	}
	if _, zone := w.chatLocation(w.mustUserByID(m.userID)); zone != "Europe/Berlin" {
		t.Errorf("a rejected zone overwrote the stored one: %q", zone)
	}

	w.setTimezone(m, "")
	if got := reply(); !strings.Contains(got, "Europe/Berlin") {
		t.Errorf("the bare command misses the zone: %q", got)
	}
}

// TestSetZoneNames pins what fills the map, at the assignment rather than beside it,
// so no path stores a table unchecked or unloadable.
// The names come from the server, since only a real one can be loaded.
func TestSetZoneNames(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	full := w.db.TimezoneNames()
	if len(full) < minZoneNames {
		t.Fatalf("the server offered %d names, too few to test the floor with", len(full))
	}

	panics := func(names map[string]string) (panicked bool) {
		defer func() { panicked = recover() != nil }()
		(&worker{}).setZoneNames(names)
		return
	}
	if !panics(map[string]string{"utc": utcZone}) {
		t.Error("a table of one name was accepted")
	}
	if !panics(nil) {
		t.Error("an empty table was accepted")
	}

	// A name the server offers and the binary cannot load is dropped rather than kept,
	// so the page cannot offer a row the save would refuse.
	offered := map[string]string{"europe/atlantis": "Europe/Atlantis"}
	for lower, canonical := range full {
		offered[lower] = canonical
	}
	w.setZoneNames(offered)
	if len(w.zoneNames) != len(full) {
		t.Errorf("stored %d zones from %d offered, want %d", len(w.zoneNames), len(offered), len(full))
	}
	if _, held := w.zoneNames["europe/atlantis"]; held {
		t.Error("a zone this binary cannot load was stored")
	}
}
