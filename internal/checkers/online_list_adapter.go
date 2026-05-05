package checkers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"

	"github.com/bcmk/siren/v2/lib/cmdlib"
)

// OnlineListAdapter is a Checker that delegates online-list queries to a
// remote daemon over HTTP. It adapts the daemon's HTTP/JSON response (an
// OnlineListResults document) to the cmdlib.Checker interface the bot uses.
//
// Site-specific behaviour (nickname extraction, subject support) is supplied
// via struct fields rather than embedded in the type, so a single adapter
// implementation can serve any site that has a backing daemon.
type OnlineListAdapter struct {
	cmdlib.CheckerCommon
	// OnlineURL is the daemon's base URL, e.g. "http://adapter-mfc:8080".
	// The adapter appends /online and /status itself.
	OnlineURL string
	// NicknameRegex extracts the model nickname from a URL (used by
	// NicknamePreprocessing). Optional — if nil, input is used as-is.
	NicknameRegex *regexp.Regexp
	// NicknameValidator validates a preprocessed nickname (returned by
	// NicknameRegexp). Required — the bot uses it to reject bad input
	// before checking status.
	NicknameValidator  *regexp.Regexp
	SubjectIsSupported bool
}

var _ cmdlib.Checker = &OnlineListAdapter{}

// Init forwards to CheckerCommon.Init and pulls OnlineURL from
// config.UsersOnlineEndpoints[0] when not already set. Fails fast if
// neither source supplies a URL — running without one is a deploy bug.
func (c *OnlineListAdapter) Init(config cmdlib.CheckerConfig) {
	c.CheckerCommon.Init(config)
	if c.OnlineURL == "" && len(config.UsersOnlineEndpoints) > 0 {
		c.OnlineURL = config.UsersOnlineEndpoints[0]
	}
	if c.OnlineURL == "" {
		log.Fatal("OnlineListAdapter requires users_online_endpoint")
	}
}

// NicknamePreprocessing extracts a canonical nickname from a URL or raw name
// and lowercases it (all sites we adapt to are case-insensitive).
func (c *OnlineListAdapter) NicknamePreprocessing(name string) string {
	if c.NicknameRegex != nil {
		if m := c.NicknameRegex.FindStringSubmatch(name); len(m) == 2 {
			name = m[1]
		}
	}
	return strings.ToLower(name)
}

// NicknameRegexp returns the validator the bot uses to reject malformed
// nicknames before issuing status queries.
func (c *OnlineListAdapter) NicknameRegexp() *regexp.Regexp {
	return c.NicknameValidator
}

// SubjectSupported reflects the per-site config flag.
func (c *OnlineListAdapter) SubjectSupported() bool { return c.SubjectIsSupported }

// Capabilities reports the status surfaces the adapter implements.
func (*OnlineListAdapter) Capabilities() cmdlib.Capabilities {
	return cmdlib.Capabilities{
		QueryOnlineStreamers:          true,
		QueryFixedListOnlineStreamers: false,
		QueryFixedListStatuses:        false,
		QueryStatus:                   true,
	}
}

// QueryOnlineStreamers fetches the online list from the daemon.
func (c *OnlineListAdapter) QueryOnlineStreamers() (
	map[string]cmdlib.StreamerInfo,
	error,
) {
	client := c.ClientsLoop.NextClient()
	endpoint := c.OnlineURL + "/online"
	resp, buf, err := cmdlib.OnlineQuery(endpoint, client, c.Headers)
	if err != nil {
		return nil, fmt.Errorf("cannot query %s, %v", endpoint, err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query status, %d", resp.StatusCode)
	}
	// Daemon serialises an OnlineListResults via its MarshalJSON; we only
	// need the streamers map plus the failed flag.
	var result struct {
		Streamers map[string]cmdlib.StreamerInfo `json:"streamers"`
		Failed    bool                           `json:"failed"`
	}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}
	if result.Failed {
		return nil, errors.New("daemon reported failed result")
	}
	if len(result.Streamers) == 0 {
		return nil, errors.New("zero online models reported")
	}
	return result.Streamers, nil
}

// QueryStatus queries the daemon's /status?name=<nickname> route and
// returns the StreamerInfoWithStatus it reports. Per checker convention
// any transport or parse failure is logged and surfaced as StatusUnknown
// rather than an error so the bot's status loop keeps running.
func (c *OnlineListAdapter) QueryStatus(nickname string) (cmdlib.StreamerInfoWithStatus, error) {
	client := c.ClientsLoop.NextClient()
	endpoint := c.OnlineURL + "/status?name=" + url.QueryEscape(nickname)
	resp, buf, err := cmdlib.OnlineQuery(endpoint, client, c.Headers)
	if err != nil {
		cmdlib.Lerr("[%v] cannot query %s, %v", client.Addr, endpoint, err)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	if resp.StatusCode != 200 {
		cmdlib.Lerr("[%v] %s returned %d", client.Addr, endpoint, resp.StatusCode)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	var result cmdlib.StreamerInfoWithStatus
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		cmdlib.Lerr("[%v] cannot parse %s response, %v", client.Addr, endpoint, err)
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	return result, nil
}

// QueryFixedListOnlineStreamers is not implemented for online-list adapters.
func (c *OnlineListAdapter) QueryFixedListOnlineStreamers(
	_ []string,
	_ cmdlib.CheckMode,
) (map[string]cmdlib.StreamerInfo, error) {
	return nil, cmdlib.ErrNotImplemented
}

// QueryFixedListStatuses is not implemented for online-list adapters.
func (c *OnlineListAdapter) QueryFixedListStatuses(
	_ []string,
	_ cmdlib.CheckMode,
) (map[string]cmdlib.StreamerInfoWithStatus, error) {
	return nil, cmdlib.ErrNotImplemented
}
