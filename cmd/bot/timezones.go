package main

import (
	"embed"
	"encoding/xml"
	"strings"
	"sync"
	"time"

	"github.com/bcmk/siren/v5/lib/cmdlib"
)

// tzdata holds upstream data verbatim; scripts/update-timezones refreshes it.

//go:embed tzdata
var tzdataFS embed.FS

var weekStarts = sync.OnceValue(mergeTzdata)

func tzdataFile(name string) string {
	body, err := tzdataFS.ReadFile("tzdata/" + name)
	cmdlib.CheckErr(err)
	return string(body)
}

// mergeTzdata maps every zone spelling to the day its country opens the week on.
// Bad data stops the bot: the files are committed, so a break here is a bad refresh.
func mergeTzdata() map[string]time.Weekday {
	// Countries, most precise source last so it wins:
	// zone1970.tab approximates a merged row by its first country,
	// zone.tab then restores each name's own country
	// (Africa/Accra is Ghana's, whatever rules it follows).
	countries := map[string]string{}
	for _, file := range []string{"zone1970.tab", "zone.tab"} {
		for line := range strings.Lines(tzdataFile(file)) {
			if strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Split(strings.TrimSuffix(line, "\n"), "\t")
			if len(fields) < 3 {
				continue
			}
			country, _, _ := strings.Cut(fields[0], ",")
			countries[fields[2]] = country
		}
	}
	// etcetera holds the countryless Etc family.
	for line := range strings.Lines(tzdataFile("etcetera")) {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "Zone" {
			countries[fields[1]] = ""
		}
	}
	// An old spelling with no row of its own takes its target's country.
	// Two passes settle a link to a link.
	var links [][]string
	for _, file := range []string{"backward", "etcetera"} {
		for line := range strings.Lines(tzdataFile(file)) {
			if fields := strings.Fields(line); len(fields) >= 3 && fields[0] == "Link" {
				links = append(links, fields)
			}
		}
	}
	for range 2 {
		for _, link := range links {
			if _, own := countries[link[2]]; own {
				continue
			}
			if country, ok := countries[link[1]]; ok {
				countries[link[2]] = country
			}
		}
	}
	for _, link := range links {
		if _, ok := countries[link[2]]; !ok {
			cmdlib.Lfatalf("unresolved timezone link: name = %s, target = %s", link[2], link[1])
		}
	}

	var weekData struct {
		FirstDay []struct {
			Day         string `xml:"day,attr"`
			Territories string `xml:"territories,attr"`
			Alt         string `xml:"alt,attr"`
		} `xml:"firstDay"`
	}
	cmdlib.CheckErr(xml.Unmarshal([]byte(tzdataFile("weekData.xml")), &weekData))
	firstDay := map[string]string{}
	for _, row := range weekData.FirstDay {
		// The one alt row is a British variant, not the British convention.
		if row.Alt != "" {
			continue
		}
		for _, territory := range strings.Fields(row.Territories) {
			firstDay[territory] = row.Day
		}
	}
	// An empty set would quietly open every week on Monday.
	if len(firstDay) == 0 {
		cmdlib.Lfatalf("no firstDay rows in weekData.xml")
	}
	days := map[string]time.Weekday{
		"mon": time.Monday, "sun": time.Sunday, "sat": time.Saturday, "fri": time.Friday,
	}

	table := make(map[string]time.Weekday, len(countries))
	for name, country := range countries {
		token, ok := firstDay[country]
		if country == "" || !ok {
			token = "mon"
		}
		day, ok := days[token]
		if !ok {
			cmdlib.Lfatalf("unknown week start: timezone = %s, day = %s", name, token)
		}
		table[name] = day
	}
	return table
}

// weekStart is the day the chat's week opens on,
// Monday for a zone off the table, as a stored spelling the table no longer offers can be.
func weekStart(loc *time.Location) time.Weekday {
	if day, ok := weekStarts()[loc.String()]; ok {
		return day
	}
	return time.Monday
}
