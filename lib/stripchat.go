package lib

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
)

// StripchatChecker implements a checker for Stripchat
type StripchatChecker struct{ CheckerCommon }

var _ Checker = &StripchatChecker{}

type stripchatModel struct {
	Username    string `json:"username"`
	SnapshotURL string `json:"snapshotUrl"`
}

type stripchatResponse struct {
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
func (c *StripchatChecker) CheckStatusSingle(modelID string) StatusKind {
	client := c.clientsLoop.nextClient()
	ctx, cancel := chromedp.NewContext(context.Background(), chromedp.WithLogf(Ldbg))
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
		Lerr("[%v] cannot open a page for model %s, %v", client.Addr, modelID, err)
		return StatusUnknown
	}
	if len(videoNode) > 0 {
		if c.Dbg {
			Ldbg("video found")
		}
		return StatusOnline
	}
	if len(notFoundNode) > 0 {
		if c.Dbg {
			Ldbg(".not-found-error found")
		}
		return StatusNotFound
	}
	if len(disabledNode) > 0 {
		if c.Dbg {
			Ldbg(".account-disabled-page found")
		}
		return StatusDenied
	}
	if len(statusNode) > 0 {
		classes := strings.Split(statusNode[0].AttributeValue("class"), " ")
		for _, class := range classes {
			if statusesOffline[class] {
				if c.Dbg {
					Ldbg("offline status found")
				}
				return StatusOffline
			}
			if statusesOnline[class] {
				if c.Dbg {
					Ldbg("online status found")
				}
				return StatusOnline
			}
		}
		Lerr("[%v] unknown status for model %s, %v", client.Addr, modelID, classes)
	}
	Lerr("[%v] unknown status for model %s", client.Addr, modelID)
	return StatusUnknown
}

// checkEndpoint returns Stripchat online models
func (c *StripchatChecker) checkEndpoint(endpoint string) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	client := c.clientsLoop.nextClient()
	onlineModels = map[string]StatusKind{}
	images = map[string]string{}
	resp, buf, err := onlineQuery(endpoint, client, c.Headers)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("query status, %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
	parsed := &stripchatResponse{}
	err = decoder.Decode(parsed)
	if err != nil {
		if c.Dbg {
			Ldbg("response: %s", buf.String())
		}
		return nil, nil, fmt.Errorf("cannot parse response, %v", err)
	}
	for _, m := range parsed.Models {
		modelID := strings.ToLower(m.Username)
		onlineModels[modelID] = StatusOnline
		images[modelID] = m.SnapshotURL
	}
	return
}

// CheckStatusesMany returns Stripchat online models
func (c *StripchatChecker) CheckStatusesMany(QueryModelList, CheckMode) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	return checkEndpoints(c, c.UsersOnlineEndpoints, c.Dbg)
}

// Start starts a daemon
func (c *StripchatChecker) Start()                 { c.startFullCheckerDaemon(c) }
func (c *StripchatChecker) createUpdater() Updater { return c.createFullUpdater(c) }
