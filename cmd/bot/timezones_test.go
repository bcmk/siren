package main

import (
	"archive/zip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestTableZonesShipWithTheBinary opens a toolchain's zoneinfo.zip, the copy time/tzdata embeds,
// which alone answers production, where the image installs no zone files.
// Everywhere tests run the host's own zone directory answers first, so it cannot measure that lag,
// and the host toolchain need not be the builder's, so the Dockerfile says which one to ask.
func TestTableZonesShipWithTheBinary(t *testing.T) {
	t.Parallel()
	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile.bot"))
	if err != nil {
		t.Fatalf("cannot read Dockerfile.bot: %v", err)
	}
	builder := regexp.MustCompile(`FROM golang:(\S+)-alpine`).FindSubmatch(dockerfile)
	if builder == nil {
		t.Fatal("Dockerfile.bot names no golang builder")
	}
	ask := exec.Command("go", "env", "GOROOT")
	ask.Env = append(os.Environ(), "GOTOOLCHAIN=go"+string(builder[1]))
	goroot, err := ask.Output()
	if err != nil {
		t.Fatalf("cannot ask go %s for GOROOT: %v", builder[1], err)
	}
	archive, err := zip.OpenReader(
		filepath.Join(strings.TrimSpace(string(goroot)), "lib", "time", "zoneinfo.zip"))
	if err != nil {
		t.Fatalf("cannot open the toolchain's zoneinfo.zip: %v", err)
	}
	defer func() { checkErr(archive.Close()) }()
	shipped := make(map[string]bool, len(archive.File))
	for _, file := range archive.File {
		shipped[file.Name] = true
	}
	for zone := range weekStarts() {
		if !shipped[zone] {
			t.Errorf("the table offers a zone the embedded tzdata misses: %s", zone)
		}
	}
}

// TestWeekStart reads the embedded table through the one lookup the grid uses:
// a canonical zone, an old spelling, and a countryless zone falling to Monday.
func TestWeekStart(t *testing.T) {
	t.Parallel()
	tests := []struct {
		zone string
		day  time.Weekday
	}{
		{"Europe/Berlin", time.Monday},
		{"Europe/Moscow", time.Monday},
		{"America/Santiago", time.Monday},
		{"America/New_York", time.Sunday},
		{"Asia/Jerusalem", time.Sunday},
		{"Africa/Cairo", time.Saturday},
		{"Indian/Maldives", time.Friday},
		// Zones merged into another country's rules keep their own country's day:
		// Kerguelen follows Maldives clocks, never its Friday.
		{"Indian/Kerguelen", time.Monday},
		{"Atlantic/Reykjavik", time.Sunday},
		// Old spellings, two of which production chats hold today.
		{"US/Eastern", time.Sunday},
		{"Asia/Calcutta", time.Sunday},
		{"Brazil/West", time.Sunday},
		{"Israel", time.Sunday},
		// Zones naming no country.
		{"UTC", time.Monday},
		{"Etc/GMT-3", time.Monday},
	}
	for _, tc := range tests {
		t.Run(tc.zone, func(t *testing.T) {
			t.Parallel()
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatalf("cannot load %s: %v", tc.zone, err)
			}
			if got := weekStart(loc); got != tc.day {
				t.Errorf("week start = %s, want %s", got, tc.day)
			}
		})
	}
}
