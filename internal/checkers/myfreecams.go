package checkers

import "regexp"

// myFreeCamsModelRegexp extracts a model nickname from a MyFreeCams URL.
var myFreeCamsModelRegexp = regexp.MustCompile(
	`^(?:https?://)?(?:[A-Za-z]+\.)?myfreecams\.com` +
		`(?:/models)?/?#?/?([A-Za-z0-9_]+)/?(?:\?.*)?$`)

// myFreeCamsNicknameRegexp validates a preprocessed MyFreeCams nickname
// (lowercased letters/digits/underscore).
var myFreeCamsNicknameRegexp = regexp.MustCompile(`^[a-z0-9_]+$`)

// NewMyFreeCamsChecker returns an OnlineListAdapter for MyFreeCams.
func NewMyFreeCamsChecker() *OnlineListAdapter {
	return &OnlineListAdapter{
		siteName:          "myfreecams",
		NicknameRegex:     myFreeCamsModelRegexp,
		NicknameValidator: myFreeCamsNicknameRegexp,
		SupportsSubject:   true,
	}
}
