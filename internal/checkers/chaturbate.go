package checkers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/bcmk/siren/v3/lib/cmdlib"
)

// ChaturbateCheckerConfig is the per-site config for Chaturbate.
type ChaturbateCheckerConfig struct {
	BaseCheckerConfig   `mapstructure:",squash"`
	UsersOnlineEndpoint string          `mapstructure:"users_online_endpoint"`
	Headers             [][2]string     `mapstructure:"headers"`
	Proxies             []cmdlib.Secret `mapstructure:"proxies"`
}

func (c *ChaturbateCheckerConfig) validate() error {
	if err := c.validateBase(); err != nil {
		return err
	}
	if c.UsersOnlineEndpoint == "" {
		return errors.New("configure users_online_endpoint")
	}
	if len(c.Proxies) == 0 {
		return errors.New("configure proxies")
	}
	for i, p := range c.Proxies {
		if p == "" {
			return fmt.Errorf("proxies[%d] is empty", i)
		}
		if _, err := url.Parse(string(p)); err != nil {
			return fmt.Errorf("proxies[%d]: invalid URL, %v", i, err)
		}
	}
	return nil
}

// ChaturbateChecker implements a checker for Chaturbate
type ChaturbateChecker struct {
	BaseChecker[*ChaturbateCheckerConfig]
	proxyClients []*http.Client
	nextProxy    atomic.Uint64
}

var _ Checker = &ChaturbateChecker{}

// Site returns the site name.
func (*ChaturbateChecker) Site() string { return "chaturbate" }

// Init loads chaturbate-checker.json.
func (c *ChaturbateChecker) Init(checkerCfgPath string) error {
	if err := c.ensureUninitialised(); err != nil {
		return err
	}
	cfg := &ChaturbateCheckerConfig{}
	if err := readCheckerConfig(cfg, c.Site(), checkerCfgPath); err != nil {
		return err
	}
	c.BaseChecker = NewBaseChecker(cfg)
	for i, p := range cfg.Proxies {
		u, err := url.Parse(string(p))
		if err != nil {
			return fmt.Errorf("proxies[%d]: invalid URL, %v", i, err)
		}
		c.proxyClients = append(c.proxyClients, cmdlib.HTTPClientWithProxy(cfg.Timeout(), u))
	}
	return nil
}

var chaturbateModelRegexp = regexp.MustCompile(`^(?:https?://)?(?:[A-Za-z]+\.)?chaturbate\.com(?:/p|/b)?/([A-Za-z0-9\-_@]+)/?(?:\?.*)?$`)

// NicknamePreprocessing preprocesses nickname to canonical form
func (c *ChaturbateChecker) NicknamePreprocessing(name string) string {
	m := chaturbateModelRegexp.FindStringSubmatch(name)
	if len(m) == 2 {
		name = m[1]
	}
	return strings.ToLower(name)
}

type chaturbateModel struct {
	Username    string `json:"username"`
	ImageURL    string `json:"image_url"`
	CurrentShow string `json:"current_show"`
	NumUsers    int    `json:"num_users"`
	RoomSubject string `json:"room_subject"`
}

type chaturbateResponse struct {
	RoomStatus string `json:"room_status"`
	Status     *int   `json:"status"`
	Code       string `json:"code"`
}

func (c *ChaturbateChecker) pickProxyClient() *http.Client {
	idx := c.nextProxy.Add(1) - 1
	return c.proxyClients[idx%uint64(len(c.proxyClients))]
}

// QueryStatus checks Chaturbate model status
func (c *ChaturbateChecker) QueryStatus(modelID string) (cmdlib.StreamerInfoWithStatus, error) {
	client := c.pickProxyClient()
	req, err := http.NewRequest("GET", fmt.Sprintf("https://chaturbate.com/api/biocontext/%s/?", modelID), nil)
	cmdlib.CheckErr(err)
	for _, h := range c.Cfg.Headers {
		req.Header.Set(h[0], h[1])
	}
	resp, err := client.Do(req)
	if err != nil {
		cmdlib.Lerr("cannot send a query, %v", err)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	defer cmdlib.CloseBody(resp.Body)
	cmdlib.Ldbg("query status for %s: %d", req.URL, resp.StatusCode)
	if resp.StatusCode == 404 {
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusNotFound}, nil
	}
	buf := bytes.Buffer{}
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		cmdlib.Lerr("cannot read response for model %s, %v", modelID, err)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	parsed := &chaturbateResponse{}
	if err := json.Unmarshal(buf.Bytes(), parsed); err != nil {
		cmdlib.Lerr("cannot parse response for model %s, %v", modelID, err)
		cmdlib.Ldbg("response: %s", buf.String())
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	if parsed.Status != nil {
		return cmdlib.StreamerInfoWithStatus{Status: chaturbateStatus(parsed.Code)}, nil
	}
	return cmdlib.StreamerInfoWithStatus{Status: chaturbateRoomStatus(parsed.RoomStatus)}, nil
}

func chaturbateStatus(status string) cmdlib.StatusKind {
	switch status {
	case "access-denied":
		return cmdlib.StatusDenied
	case "unauthorized":
		return cmdlib.StatusDenied
	}
	return cmdlib.StatusUnknown
}

func chaturbateRoomStatus(roomStatus string) cmdlib.StatusKind {
	switch roomStatus {
	case "public":
		return cmdlib.StatusOnline
	case "private":
		return cmdlib.StatusOnline
	case "group":
		return cmdlib.StatusOnline
	case "hidden":
		return cmdlib.StatusOnline
	case "connecting":
		return cmdlib.StatusOnline
	case "password protected":
		return cmdlib.StatusOnline
	case "away":
		return cmdlib.StatusOffline
	case "offline":
		return cmdlib.StatusOffline
	}
	cmdlib.Lerr("cannot parse room status \"%s\"", roomStatus)
	return cmdlib.StatusUnknown
}

func chaturbateShowKind(currentShow string) cmdlib.ShowKind {
	switch currentShow {
	case "public":
		return cmdlib.ShowPublic
	case "hidden":
		return cmdlib.ShowHidden
	case "private":
		return cmdlib.ShowPrivate
	case "away":
		return cmdlib.ShowAway
	}
	return cmdlib.ShowUnknown
}

// QueryOnlineStreamers returns Chaturbate online models
func (c *ChaturbateChecker) QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error) {
	streamers := map[string]cmdlib.StreamerInfo{}
	resp, buf, err := cmdlib.OnlineQuery(c.Cfg.UsersOnlineEndpoint, c.Client, c.Cfg.Headers)
	if err != nil {
		return nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query status, %d", resp.StatusCode)
	}
	var parsed []chaturbateModel
	err = json.Unmarshal(buf.Bytes(), &parsed)
	if err != nil {
		cmdlib.Ldbg("response: %s", buf.String())
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}
	for _, m := range parsed {
		modelID := strings.ToLower(m.Username)
		viewers := m.NumUsers
		streamers[modelID] = cmdlib.StreamerInfo{
			ImageURL: m.ImageURL,
			Viewers:  &viewers,
			ShowKind: chaturbateShowKind(m.CurrentShow),
			Subject:  m.RoomSubject,
		}
	}
	if len(streamers) == 0 {
		return nil, errors.New("zero online models reported")
	}
	return streamers, nil
}

// QueryFixedListOnlineStreamers is not implemented for online list checkers
func (c *ChaturbateChecker) QueryFixedListOnlineStreamers([]string, cmdlib.CheckMode) (map[string]cmdlib.StreamerInfo, error) {
	return nil, ErrNotImplemented
}

// Capabilities lists the surfaces Chaturbate exposes for dispatch.
func (*ChaturbateChecker) Capabilities() Capabilities {
	return Capabilities{
		SupportsQueryOnlineStreamers:          true,
		SupportsQueryFixedListOnlineStreamers: false,
		SupportsQueryFixedListStatuses:        false,
		SupportsQueryStatus:                   true,
		SupportsCLI:                           true,
		SupportsSubject:                       true,
	}
}
