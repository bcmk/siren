package checkers

import "regexp"

// myFreeCamsModelRegexp extracts a model nickname from a MyFreeCams URL.
// Lives here (rather than in myfreecams.go) so the old in-process checker
// can be deleted without removing the regex used by NewMyFreeCamsAdapter.
var myFreeCamsModelRegexp = regexp.MustCompile(
	`^(?:https?://)?(?:[A-Za-z]+\.)?myfreecams\.com` +
		`(?:/models)?/?#?/?([A-Za-z0-9_]+)/?(?:\?.*)?$`)

// myFreeCamsNicknameRegexp validates a preprocessed MyFreeCams nickname
// (lowercased letters/digits/underscore).
var myFreeCamsNicknameRegexp = regexp.MustCompile(`^[a-z0-9_]+$`)

// NewMyFreeCamsAdapter returns an OnlineListAdapter pre-configured for
// MyFreeCams. The OnlineURL field is left empty; it gets filled from
// CheckerConfig.UsersOnlineEndpoints[0] during Init (i.e., from the bot
// config or the CLI `-e` flag).
func NewMyFreeCamsAdapter() *OnlineListAdapter {
	return &OnlineListAdapter{
		NicknameRegex:      myFreeCamsModelRegexp,
		NicknameValidator:  myFreeCamsNicknameRegexp,
		SubjectIsSupported: true,
	}
}
