package checkers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bcmk/siren/lib/cmdlib"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
)

// StripchatChecker implements a checker for Stripchat
type StripchatChecker struct{ cmdlib.CheckerCommon }

var _ cmdlib.Checker = &StripchatChecker{}

type stripchatModel struct {
	Username    string `json:"username"`
	SnapshotURL string `json:"snapshotUrl"`
}

type stripchatResponse struct {
	Total  int              `json:"total"`
	Models []stripchatModel `json:"models"`
}

var statusesOffline = map[string]bool{
	"status-off": true,
}

var statusesOnline = map[string]bool{
	"status-p2p":       true,
	"status-private":   true,
	"status-groupShow": true,
	"status-idle":      true,
}

// CheckStatusSingle checks Stripchat model status
func (c *StripchatChecker) CheckStatusSingle(modelID string) cmdlib.StatusKind {
	client := c.ClientsLoop.NextClient()
	ctx, cancel := chromedp.NewContext(context.Background(), chromedp.WithLogf(cmdlib.Ldbg))
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var videoNode []*cdp.Node
	var statusNode []*cdp.Node
	var disabledNode []*cdp.Node
	var notFoundNode []*cdp.Node
	err := chromedp.Run(ctx,
		chromedp.Navigate(fmt.Sprintf("https://stripchat.com/%s", modelID)),
		chromedp.WaitVisible(`video, .vc-status, .account-disabled-page, .not-found-error`, chromedp.ByQuery),
		chromedp.Nodes(`video`, &videoNode, chromedp.AtLeast(0), chromedp.ByQuery),
		chromedp.Nodes(`.vc-status`, &statusNode, chromedp.AtLeast(0), chromedp.ByQuery),
		chromedp.Nodes(`.account-disabled-page`, &disabledNode, chromedp.AtLeast(0), chromedp.ByQuery),
		chromedp.Nodes(`.not-found-error`, &notFoundNode, chromedp.AtLeast(0), chromedp.ByQuery),
	)
	if err != nil {
		cmdlib.Lerr("[%v] cannot open a page for model %s, %v", client.Addr, modelID, err)
		return cmdlib.StatusUnknown
	}
	if len(videoNode) > 0 {
		if c.Dbg {
			cmdlib.Ldbg("video found")
		}
		return cmdlib.StatusOnline
	}
	if len(notFoundNode) > 0 {
		if c.Dbg {
			cmdlib.Ldbg(".not-found-error found")
		}
		return cmdlib.StatusNotFound
	}
	if len(disabledNode) > 0 {
		if c.Dbg {
			cmdlib.Ldbg(".account-disabled-page found")
		}
		return cmdlib.StatusDenied
	}
	if len(statusNode) > 0 {
		classes := strings.Split(statusNode[0].AttributeValue("class"), " ")
		for _, class := range classes {
			if statusesOffline[class] {
				if c.Dbg {
					cmdlib.Ldbg("offline status found")
				}
				return cmdlib.StatusOffline
			}
			if statusesOnline[class] {
				if c.Dbg {
					cmdlib.Ldbg("online status found")
				}
				return cmdlib.StatusOnline
			}
		}
		cmdlib.Lerr("[%v] unknown status for model %s, %v", client.Addr, modelID, classes)
	}
	cmdlib.Lerr("[%v] unknown status for model %s", client.Addr, modelID)
	return cmdlib.StatusUnknown
}

// CheckEndpoint returns Stripchat online models
func (c *StripchatChecker) CheckEndpoint(endpoint string) (
	onlineModels map[string]cmdlib.StatusKind,
	images map[string]string,
	err error,
) {
	onlineModels = map[string]cmdlib.StatusKind{}
	images = map[string]string{}
	maxQueries := 20
	totalModels := 0
	limit := 1000
	offsetK := 900 // It overlaps limit
	for currentQuery := 0; currentQuery < maxQueries; currentQuery++ {
		client := c.ClientsLoop.NextClient()

		request, err := url.Parse(endpoint)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot parse endpoint %q", endpoint)
		}

		q := request.Query()
		q.Set("offset", strconv.Itoa(currentQuery*offsetK))
		q.Set("limit", strconv.Itoa(limit))

		request.RawQuery = q.Encode()

		resp, buf, err := cmdlib.OnlineQuery(request.String(), client, c.Headers)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot send a query, %v", err)
		}
		if resp.StatusCode != 200 {
			return nil, nil, fmt.Errorf("query status %d", resp.StatusCode)
		}
		decoder := json.NewDecoder(io.NopCloser(bytes.NewReader(buf.Bytes())))
		parsed := &stripchatResponse{}
		err = decoder.Decode(parsed)
		if err != nil {
			if c.Dbg {
				cmdlib.Ldbg("response: %s", buf.String())
			}
			return nil, nil, fmt.Errorf("cannot parse response, %v", err)
		}
		if totalModels == 0 {
			totalModels = parsed.Total
		}
		for _, m := range parsed.Models {
			modelID := strings.ToLower(m.Username)
			onlineModels[modelID] = cmdlib.StatusOnline
			images[modelID] = m.SnapshotURL
		}
		if (currentQuery+1)*offsetK > totalModels {
			break
		}
	}
	return
}

// CheckStatusesMany returns Stripchat online models
func (c *StripchatChecker) CheckStatusesMany(cmdlib.QueryModelList, cmdlib.CheckMode) (
	onlineModels map[string]cmdlib.StatusKind,
	images map[string]string,
	err error,
) {
	return cmdlib.CheckEndpoints(c, c.UsersOnlineEndpoints, c.Dbg)
}

// Start starts a daemon
func (c *StripchatChecker) Start() { c.StartFullCheckerDaemon(c) }

// CreateUpdater creates an updater
func (c *StripchatChecker) CreateUpdater() cmdlib.Updater { return c.CreateFullUpdater(c) }
