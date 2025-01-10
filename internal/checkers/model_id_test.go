package checkers

import "testing"

func TestModelID(t *testing.T) {
	if !TwitchModelIDRegexp.MatchString("test") {
		t.Error("unexpected results")
	}
	if !TwitchModelIDRegexp.MatchString("@test") {
		t.Error("unexpected results")
	}
	if TwitchModelIDRegexp.MatchString("@test@test") {
		t.Error("unexpected results")
	}
	if TwitchModelIDRegexp.MatchString("test@test") {
		t.Error("unexpected results")
	}
	if TwitchCanonicalModelID("@test") != "test" {
		t.Error("unexpected results")
	}
}
