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
	SnapshotURL string `json:"snapshot_url"`
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
func CheckModelStripchat(client *Client, modelID string, headers [][2]string, dbg bool) StatusKind {
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

// StartStripchatAPIChecker starts a checker for Stripchat
func StartStripchatAPIChecker(
	usersOnlineEndpoint []string,
	clients []*Client,
	headers [][2]string,
	intervalMs int,
	dbg bool,
) (
	statusRequests chan StatusRequest,
	output chan []StatusUpdate,
	elapsedCh chan time.Duration) {

	statusRequests = make(chan StatusRequest)
	output = make(chan []StatusUpdate)
	elapsedCh = make(chan time.Duration)
	clientIdx := 0
	clientsNum := len(clients)
	go func() {
		for request := range statusRequests {
			hash := map[string]StatusUpdate{}
			updates := []StatusUpdate{}
			for _, endpoint := range usersOnlineEndpoint {
				client := clients[clientIdx]
				clientIdx++
				if clientIdx == clientsNum {
					clientIdx = 0
				}

				resp, buf, elapsed, err := onlineQuery(endpoint, client, headers)
				elapsedCh <- elapsed
				if err != nil {
					Lerr("[%v] cannot send a query, %v", client.Addr, err)
					output <- nil
					continue
				}
				if resp.StatusCode != 200 {
					Lerr("[%v] query status, %d", client.Addr, resp.StatusCode)
					output <- nil
					continue
				}
				decoder := json.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
				parsed := &stripchatResponse{}
				err = decoder.Decode(parsed)
				if err != nil {
					Lerr("[%v] cannot parse response, %v", client.Addr, err)
					if dbg {
						Ldbg("response: %s", buf.String())
					}
					output <- nil
					continue
				}
				for _, m := range parsed.Models {
					modelID := strings.ToLower(m.Username)
					hash[modelID] = StatusUpdate{ModelID: modelID, Status: StatusOnline, Image: m.SnapshotURL}
				}
			}
			for _, statusUpdate := range hash {
				updates = append(updates, statusUpdate)
			}
			for _, modelID := range request.KnownModels {
				if _, ok := hash[modelID]; !ok {
					updates = append(updates, StatusUpdate{ModelID: modelID, Status: StatusOffline})
				}
			}
			output <- updates
		}
	}()
	return
}
