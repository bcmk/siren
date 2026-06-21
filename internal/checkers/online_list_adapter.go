package checkers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/bcmk/siren/v2/lib/cmdlib"
)

// AdapterCheckerConfig configures an OnlineListAdapter: the base URL of
// the backing daemon plus the usual HTTP knobs.
type AdapterCheckerConfig struct {
	BaseCheckerConfig `mapstructure:",squash"`
	// BaseURL is the daemon's base URL, e.g. "http://adapter-mfc:8080".
	// The adapter appends /online and /status itself.
	BaseURL string `mapstructure:"base_url"`
}

func (c *AdapterCheckerConfig) validate() error {
	if err := c.validateBase(); err != nil {
		return err
	}
	// The adapter builds endpoints as BaseURL + "/online" etc., so a
	// trailing slash would yield a double slash. Normalise it away.
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	if c.BaseURL == "" {
		return errors.New("configure base_url")
	}
	return nil
}

// OnlineListAdapter is a Checker that delegates to a remote daemon over
// HTTP. It serves two surfaces — bulk online list via /online (an
// OnlineListResults document) and per-streamer status via /status — and
// adapts both to the Checker interface the bot uses.
//
// Site-specific behaviour (nickname extraction, subject support) is supplied
// via struct fields rather than embedded in the type, so a single adapter
// implementation can serve any site that has a backing daemon.
//
// Construction is two-phase: a per-site constructor (e.g.
// NewMyFreeCamsChecker) sets siteName and the nickname/subject fields,
// then Init populates the embedded BaseChecker from the checker config.
// Fields set by the constructor survive Init.
type OnlineListAdapter struct {
	BaseChecker[*AdapterCheckerConfig]
	siteName string
	// NicknameRegex extracts the model nickname from a URL (used by
	// NicknamePreprocessing). Optional — if nil, input is used as-is.
	NicknameRegex *regexp.Regexp
	// NicknameValidator validates a preprocessed nickname (returned by
	// NicknameRegexp). Required — the bot uses it to reject bad input
	// before checking status.
	NicknameValidator *regexp.Regexp
	// SupportsSubject is set by the constructor when the backing daemon
	// surfaces room subjects; surfaced via Capabilities.
	SupportsSubject bool
}

var _ Checker = &OnlineListAdapter{}

// Site returns the site name.
func (c *OnlineListAdapter) Site() string { return c.siteName }

// Init loads <site>-checker.json.
func (c *OnlineListAdapter) Init(checkerCfgPath string) error {
	if err := c.ensureUninitialised(); err != nil {
		return err
	}
	cfg := &AdapterCheckerConfig{}
	if err := readCheckerConfig(cfg, c.Site(), checkerCfgPath); err != nil {
		return err
	}
	c.BaseChecker = NewBaseChecker(cfg)
	return nil
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

// Capabilities lists the surfaces the adapter exposes for dispatch.
// SupportsSubject reads the instance field set by the per-site constructor.
func (c *OnlineListAdapter) Capabilities() Capabilities {
	return Capabilities{
		SupportsQueryOnlineStreamers:          true,
		SupportsQueryFixedListOnlineStreamers: false,
		SupportsQueryFixedListStatuses:        false,
		SupportsQueryStatus:                   true,
		SupportsCLI:                           false,
		SupportsSubject:                       c.SupportsSubject,
	}
}

// QueryOnlineStreamers fetches the online list from the daemon.
func (c *OnlineListAdapter) QueryOnlineStreamers() (
	map[string]cmdlib.StreamerInfo,
	error,
) {
	endpoint := c.Cfg.BaseURL + "/online"
	resp, buf, err := cmdlib.OnlineQuery(endpoint, c.Client, nil)
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
		cmdlib.Ldbg("response: %s", buf.String())
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
	endpoint := c.Cfg.BaseURL + "/status?name=" + url.QueryEscape(nickname)
	resp, buf, err := cmdlib.OnlineQuery(endpoint, c.Client, nil)
	if err != nil {
		cmdlib.Lerr("cannot query %s, %v", endpoint, err)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	if resp.StatusCode != 200 {
		cmdlib.Lerr("%s returned %d", endpoint, resp.StatusCode)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	var result cmdlib.StreamerInfoWithStatus
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		cmdlib.Lerr("cannot parse %s response, %v", endpoint, err)
		cmdlib.Ldbg("response: %s", buf.String())
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	return result, nil
}

// QueryFixedListOnlineStreamers is not implemented for online-list adapters.
func (c *OnlineListAdapter) QueryFixedListOnlineStreamers(
	_ []string,
	_ cmdlib.CheckMode,
) (map[string]cmdlib.StreamerInfo, error) {
	return nil, ErrNotImplemented
}
