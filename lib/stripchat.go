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

// CheckModelStripchat checks Stripchat model status
func CheckModelStripchat(client *Client, modelID string, headers [][2]string, dbg bool, _ map[string]string) StatusKind {
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
		if dbg {
			Ldbg("video found")
		}
		return StatusOnline
	}
	if len(notFoundNode) > 0 {
		if dbg {
			Ldbg(".not-found-error found")
		}
		return StatusNotFound
	}
	if len(disabledNode) > 0 {
		if dbg {
			Ldbg(".account-disabled-page found")
		}
		return StatusDenied
	}
	if len(statusNode) > 0 {
		classes := strings.Split(statusNode[0].AttributeValue("class"), " ")
		for _, c := range classes {
			if statusesOffline[c] {
				if dbg {
					Ldbg("offline status found")
				}
				return StatusOffline
			}
			if statusesOnline[c] {
				if dbg {
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

// StripchatOnlineAPI returns Stripchat online models
func StripchatOnlineAPI(
	endpoint string,
	client *Client,
	headers [][2]string,
	dbg bool,
	_ map[string]string,
) (
	onlineModels map[string]OnlineModel,
	err error,
) {
	onlineModels = map[string]OnlineModel{}
	resp, buf, err := onlineQuery(endpoint, client, headers)
	if err != nil {
		return nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query status, %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
	parsed := &stripchatResponse{}
	err = decoder.Decode(parsed)
	if err != nil {
		if dbg {
			Ldbg("response: %s", buf.String())
		}
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}
	for _, m := range parsed.Models {
		modelID := strings.ToLower(m.Username)
		onlineModels[modelID] = OnlineModel{ModelID: modelID, Image: m.SnapshotURL}
	}
	return
}

// StartStripchatChecker starts a checker for Chaturbate
func StartStripchatChecker(
	usersOnlineEndpoint []string,
	clients []*Client,
	headers [][2]string,
	intervalMs int,
	dbg bool,
	specificConfig map[string]string,
) (
	statusRequests chan StatusRequest,
	output chan []OnlineModel,
	errorsCh chan struct{},
	elapsedCh chan time.Duration,
) {
	return StartChecker(CheckModelStripchat, StripchatOnlineAPI, usersOnlineEndpoint, clients, headers, intervalMs, dbg, specificConfig)
}
