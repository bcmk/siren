package checkers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/bcmk/siren/v2/lib/cmdlib"
)

// StripchatChecker implements a checker for Stripchat
type StripchatChecker struct{ cmdlib.CheckerCommon }

var _ cmdlib.Checker = &StripchatChecker{}

type stripchatModel struct {
	Username     string `json:"username"`
	SnapshotURL  string `json:"snapshotUrl"`
	Status       string `json:"status"`
	ViewersCount int    `json:"viewersCount"`
}

type stripchatResponse struct {
	Total  int              `json:"total"`
	Models []stripchatModel `json:"models"`
}

type onlineModel struct {
	Username string `json:"username"`
}

type onlineResponse struct {
	Models map[string]onlineModel `json:"models"`
}

func stripchatShowKind(status string) cmdlib.ShowKind {
	switch status {
	case "public":
		return cmdlib.ShowPublic
	case "groupShow":
		return cmdlib.ShowGroup
	case "p2p", "private", "virtualPrivate":
		return cmdlib.ShowPrivate
	}
	return cmdlib.ShowUnknown
}

// CheckStatusSingle is not implemented for Stripchat
func (c *StripchatChecker) CheckStatusSingle(_ string) (cmdlib.StatusKind, error) {
	return cmdlib.StatusUnknown, cmdlib.ErrNotImplemented
}

func (c *StripchatChecker) checkOnlyOnline() (map[string]cmdlib.StreamerInfo, error) {
	endpoint := c.UsersOnlineEndpoints[0]
	userID := string(c.SpecificConfig["user_id"])
	streamers := map[string]cmdlib.StreamerInfo{}

	client := c.ClientsLoop.NextClient()

	request, err := url.Parse(endpoint + "/online")
	if err != nil {
		return nil, fmt.Errorf("cannot parse endpoint %q", endpoint)
	}

	q := request.Query()
	q.Set("userId", userID)

	request.RawQuery = q.Encode()

	resp, buf, err := cmdlib.OnlineQuery(request.String(), client, c.Headers)
	if err != nil {
		return nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query status %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.NopCloser(bytes.NewReader(buf.Bytes())))
	parsed := &onlineResponse{}
	err = decoder.Decode(parsed)
	if err != nil {
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}
	if c.Dbg {
		cmdlib.Ldbg("models count in the response: %d", len(parsed.Models))
	}
	for _, m := range parsed.Models {
		if m.Username != "" {
			modelID := strings.ToLower(m.Username)
			if _, ok := streamers[modelID]; !ok {
				streamers[modelID] = cmdlib.StreamerInfo{}
			}
		}
	}
	return streamers, nil
}

// QueryOnlineStreamers returns Stripchat online models
func (c *StripchatChecker) QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error) {
	endpoint := c.UsersOnlineEndpoints[0]
	streamers, err := c.checkOnlyOnline()
	if err != nil {
		return nil, fmt.Errorf("cannot check online models, %v", err)
	}
	// This is the actual limit, although the documentation states 1000
	limitK := 400
	chunkIter := slices.Chunk(slices.Collect(maps.Keys(streamers)), limitK)
	userID := string(c.SpecificConfig["user_id"])
	for chunk := range chunkIter {
		modelIDs := strings.Join(chunk, ",")
		client := c.ClientsLoop.NextClient()

		request, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("cannot parse endpoint %q", endpoint)
		}

		q := request.Query()
		q.Set("userId", userID)
		q.Set("modelsList", modelIDs)
		q.Set("strict", "1")
		q.Set("limit", strconv.Itoa(len(chunk)))

		request.RawQuery = q.Encode()

		resp, buf, err := cmdlib.OnlineQuery(request.String(), client, c.Headers)
		if err != nil {
			return nil, fmt.Errorf("cannot send a query, %v", err)
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("query status %d", resp.StatusCode)
		}
		decoder := json.NewDecoder(io.NopCloser(bytes.NewReader(buf.Bytes())))
		parsed := &stripchatResponse{}
		err = decoder.Decode(parsed)
		if err != nil {
			if c.Dbg {
				cmdlib.Ldbg("response: %s", buf.String())
			}
			return nil, fmt.Errorf("cannot parse response, %v", err)
		}
		if c.Dbg {
			cmdlib.Ldbg("models count in the response: %d", len(parsed.Models))
		}
		for _, m := range parsed.Models {
			if m.Username != "" {
				modelID := strings.ToLower(m.Username)
				viewers := m.ViewersCount
				streamers[modelID] = cmdlib.StreamerInfo{
					ImageURL: m.SnapshotURL,
					Viewers:  &viewers,
					ShowKind: stripchatShowKind(m.Status),
				}
			}
		}
	}
	if len(streamers) == 0 {
		return nil, errors.New("zero online models reported")
	}
	return streamers, nil
}

// QueryFixedListOnlineStreamers is not implemented for online list checkers
func (c *StripchatChecker) QueryFixedListOnlineStreamers([]string, cmdlib.CheckMode) (map[string]cmdlib.StreamerInfo, error) {
	return nil, cmdlib.ErrNotImplemented
}

// UsesFixedList returns false for online list checkers
func (c *StripchatChecker) UsesFixedList() bool { return false }
