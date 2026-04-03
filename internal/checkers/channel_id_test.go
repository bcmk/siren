package checkers

import "testing"

func TestNickname(t *testing.T) {
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
	tc := &TwitchChecker{}
	if tc.NicknamePreprocessing("@test") != "test" {
		t.Error("unexpected results")
	}
}
