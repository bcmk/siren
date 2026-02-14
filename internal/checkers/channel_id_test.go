package checkers

import "testing"

func TestStreamerID(t *testing.T) {
	if !TwitchChannelIDRegexp.MatchString("test") {
		t.Error("unexpected results")
	}
	if !TwitchChannelIDRegexp.MatchString("@test") {
		t.Error("unexpected results")
	}
	if TwitchChannelIDRegexp.MatchString("@test@test") {
		t.Error("unexpected results")
	}
	if TwitchChannelIDRegexp.MatchString("test@test") {
		t.Error("unexpected results")
	}
	if TwitchCanonicalChannelID("@test") != "test" {
		t.Error("unexpected results")
	}
}
