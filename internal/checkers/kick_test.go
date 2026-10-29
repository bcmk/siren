package checkers

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/bcmk/siren/v6/lib/cmdlib"
)

const kickDefaultAvatar = "https://kick.com/img/default-profile-pictures/default-avatar-4.webp"

// kickTransport fakes Kick's token, channels and users endpoints, and records the user ids asked.
// The channels answer one live channel, a, of user 1.
type kickTransport struct {
	asked []string
	// fail fails every users lookup, and picture replaces every profile picture when set.
	fail    bool
	picture string
}

func (t *kickTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var out any
	switch {
	case req.URL.Host == "id.kick.com" && req.URL.Path == "/oauth/token":
		out = kickTokenResponse{AccessToken: "token"}
	case req.Header.Get("Authorization") != "Bearer token":
		return kickResponse(http.StatusUnauthorized, nil)
	case req.URL.Path == "/public/v1/channels":
		live := kickChannel{Slug: "a", BroadcasterUserID: 1, Stream: &kickStream{IsLive: true}}
		out = kickChannelsResponse{Data: []kickChannel{live}}
	case req.URL.Path == "/public/v1/users":
		ids := req.URL.Query()["id"]
		t.asked = append(t.asked, ids...)
		if t.fail {
			return kickResponse(http.StatusTooManyRequests, nil)
		}
		var users []kickUser
		for _, id := range ids {
			n, err := strconv.ParseInt(id, 10, 64)
			if err != nil {
				return nil, err
			}
			picture := t.picture
			if picture == "" {
				picture = "https://p/" + id
			}
			users = append(users, kickUser{UserID: n, ProfilePicture: picture})
		}
		out = kickUsersResponse{Data: users}
	default:
		return kickResponse(http.StatusNotFound, nil)
	}
	return kickResponse(http.StatusOK, out)
}

func kickResponse(status int, out any) (*http.Response, error) {
	body, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func newTestKickChecker(tr *kickTransport) *KickChecker {
	c := &KickChecker{profileImages: map[int64]kickProfileImage{}}
	c.Cfg = &KickCheckerConfig{ClientID: "id", ClientSecret: "secret"}
	c.Client = &http.Client{Transport: tr}
	return c
}

// The checker looks up only the profile images it has not cached, or whose cache entry expired,
// and caches those it looked up for the next check.
// A failed lookup keeps an expired image and pauses the lookups.
func TestKickLookUpProfileImages(t *testing.T) {
	t.Parallel()
	fresh := time.Now().Add(time.Hour)
	expired := time.Now().Add(-time.Second)
	tests := []struct {
		name      string
		cached    map[int64]kickProfileImage
		tr        kickTransport
		checks    int
		wantAsked []string
		want      string
	}{
		{"an unknown channel is looked up", nil, kickTransport{}, 1, []string{"1"}, "https://p/1"},
		{"and cached for the next check", nil, kickTransport{}, 2, []string{"1"}, "https://p/1"},
		{"a cached one is not", map[int64]kickProfileImage{1: {url: "https://cached", expires: fresh}},
			kickTransport{}, 1, nil, "https://cached"},
		{"an expired one is looked up again", map[int64]kickProfileImage{1: {url: "https://cached", expires: expired}},
			kickTransport{}, 1, []string{"1"}, "https://p/1"},
		{"and kept when the lookup fails", map[int64]kickProfileImage{1: {url: "https://cached", expires: expired}},
			kickTransport{fail: true}, 1, []string{"1"}, "https://cached"},
		{"a failed lookup pauses the next", nil, kickTransport{fail: true}, 2, []string{"1"}, ""},
		{"a default avatar is cached as none", nil, kickTransport{picture: kickDefaultAvatar}, 2, []string{"1"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newTestKickChecker(&tc.tr)
			maps.Copy(c.profileImages, tc.cached)
			for range tc.checks {
				c.lookUpProfileImages("token", []kickChannel{{BroadcasterUserID: 1}})
			}
			if !slices.Equal(tc.tr.asked, tc.wantAsked) {
				t.Errorf("looked up %v, want %v", tc.tr.asked, tc.wantAsked)
			}
			if got := c.profileImages[1].url; got != tc.want {
				t.Errorf("profile image = %q, want %q", got, tc.want)
			}
		})
	}
}

// Expired profile images, which serve meanwhile, are refreshed a request's worth per check.
func TestKickRefreshesExpiredImagesByTheRequest(t *testing.T) {
	t.Parallel()
	tr := &kickTransport{}
	c := newTestKickChecker(tr)
	var channels []kickChannel
	for id := range int64(kickUsersPerRequest + 1) {
		c.profileImages[id+1] = kickProfileImage{url: "https://cached", expires: time.Now().Add(-time.Second)}
		channels = append(channels, kickChannel{BroadcasterUserID: id + 1})
	}
	for check, want := range []int{kickUsersPerRequest, kickUsersPerRequest + 1} {
		c.lookUpProfileImages("token", channels)
		if len(tr.asked) != want {
			t.Errorf("after check %d, looked up %d ids, want %d", check+1, len(tr.asked), want)
		}
	}
}

// A live channel's status carries its profile image.
func TestKickFixedListCarriesProfileImage(t *testing.T) {
	t.Parallel()
	c := newTestKickChecker(&kickTransport{})
	got, err := c.QueryFixedListOnlineStreamers([]string{"a"}, cmdlib.CheckOnline)
	if err != nil {
		t.Fatal(err)
	}
	if p := got["a"].ProfileImageURL; p != "https://p/1" {
		t.Errorf("profile image = %q, want %q", p, "https://p/1")
	}
}
