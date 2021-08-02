package lib

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
)

// StreamateChecker implements a checker for Streamate
type StreamateChecker struct{ CheckerCommon }

var _ Checker = &StreamateChecker{}

type descriptionsRequest struct {
	XMLName xml.Name `xml:"Descriptions"`
}

type mediaRequest struct {
	XMLName xml.Name `xml:"Media"`
	Value   string   `xml:",chardata"`
}

type staticSortRequest struct {
	XMLName xml.Name `xml:"StaticSort"`
}

type includeRequest struct {
	XMLName      xml.Name `xml:"Include"`
	Descriptions *descriptionsRequest
	Media        *mediaRequest
	StaticSort   *staticSortRequest
}

type publicProfileRequest struct {
	XMLName xml.Name `xml:"PublicProfile"`
}

type nameRequest struct {
	XMLName xml.Name `xml:"Name"`
	Value   string   `xml:",chardata"`
}

type streamTypeRequest struct {
	XMLName xml.Name `xml:"StreamType"`
	Value   string   `xml:",chardata"`
}

type constraintsRequest struct {
	XMLName       xml.Name `xml:"Constraints"`
	PublicProfile *publicProfileRequest
	Name          *nameRequest
	StreamType    *streamTypeRequest
}

type availablePerformersRequest struct {
	XMLName           xml.Name `xml:"AvailablePerformers"`
	Exact             bool     `xml:"Exact,attr"`
	PageNum           int      `xml:"PageNum,attr"`
	CountTotalResults bool     `xml:"CountTotalResults,attr"`
	Include           includeRequest
	Constraints       constraintsRequest
}

type optionsRequest struct {
	XMLName    xml.Name `xml:"Options"`
	MaxResults int      `xml:"MaxResults,attr"`
}

type streamateRequest struct {
	XMLName             xml.Name `xml:"SMLQuery"`
	Options             optionsRequest
	AvailablePerformers availablePerformersRequest
}

type fullResponse struct {
	XMLName xml.Name `xml:"Full"`
	Src     string   `xml:"Src,attr"`
}

type picResponse struct {
	XMLName xml.Name      `xml:"Pic"`
	Full    *fullResponse `xml:"Full"`
}

type mediaResponse struct {
	XMLName xml.Name     `xml:"Media"`
	Pic     *picResponse `xml:"Pic"`
}

type performerResponse struct {
	XMLName    xml.Name       `xml:"Performer"`
	Name       string         `xml:"Name,attr"`
	StreamType string         `xml:"StreamType,attr"`
	Media      *mediaResponse `xml:"Media"`
}

type availablePerformersResponse struct {
	XMLName          xml.Name            `xml:"AvailablePerformers"`
	ExactMatches     int                 `xml:"ExactMatches,attr"`
	TotalResultCount int                 `xml:"TotalResultCount,attr"`
	Performers       []performerResponse `xml:"Performer"`
}

type streamateResponse struct {
	XMLName             xml.Name `xml:"SMLResult"`
	AvailablePerformers availablePerformersResponse
}

// CheckStatusSingle checks Streamate model status
func (c *StreamateChecker) CheckStatusSingle(modelID string) StatusKind {
	client := c.clientsLoop.nextClient()
	reqData := streamateRequest{
		Options: optionsRequest{MaxResults: 1},
		AvailablePerformers: availablePerformersRequest{
			Exact:   true,
			PageNum: 1,
			Constraints: constraintsRequest{
				PublicProfile: &publicProfileRequest{},
				StreamType:    &streamTypeRequest{Value: "live,recorded,offline"},
				Name:          &nameRequest{Value: modelID},
			},
		},
	}
	output, err := xml.MarshalIndent(&reqData, "", "    ")
	CheckErr(err)
	reqString := fmt.Sprintf("%s%s\n", xml.Header, string(output))
	req, err := http.NewRequest("POST", "https://affiliate.streamate.com/SMLive/SMLResult.xml", strings.NewReader(reqString))
	CheckErr(err)
	for _, h := range c.Headers {
		req.Header.Set(h[0], h[1])
	}
	req.Header.Set("Content-Type", "text/xml")
	resp, err := client.Client.Do(req)
	if err != nil {
		Lerr("[%v] cannot send a query, %v", client.Addr, err)
		return StatusUnknown
	}
	defer func() {
		CheckErr(resp.Body.Close())
	}()
	if c.Dbg {
		Ldbg("[%v] query status for %s: %d", client.Addr, modelID, resp.StatusCode)
	}
	if resp.StatusCode == 404 {
		return StatusNotFound
	}
	buf := bytes.Buffer{}
	_, err = buf.ReadFrom(resp.Body)
	if err != nil {
		Lerr("[%v] cannot read response for model %s, %v", client.Addr, modelID, err)
		return StatusUnknown
	}
	decoder := xml.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
	parsed := &streamateResponse{}
	err = decoder.Decode(parsed)
	if err != nil {
		Lerr("[%v] cannot parse response for model %s, %v", client.Addr, modelID, err)
		if c.Dbg {
			Ldbg("response: %s", buf.String())
		}
		return StatusUnknown
	}
	if parsed.AvailablePerformers.ExactMatches != 1 || len(parsed.AvailablePerformers.Performers) != 1 {
		return StatusNotFound
	}
	switch parsed.AvailablePerformers.Performers[0].StreamType {
	case "live":
		return StatusOnline
	case "recorded", "offline":
		return StatusOffline
	}
	return StatusUnknown
}

// checkEndpoint returns Streamate online models
func (c *StreamateChecker) checkEndpoint(endpoint string) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	client := c.clientsLoop.nextClient()
	onlineModels = map[string]StatusKind{}
	images = map[string]string{}
	page := 500
	i := 0
	for i < 20 {
		i++
		reqData := streamateRequest{
			Options: optionsRequest{MaxResults: page},
			AvailablePerformers: availablePerformersRequest{
				Exact:             true,
				PageNum:           i,
				CountTotalResults: true,
				Include: includeRequest{
					Media: &mediaRequest{Value: "biopic"},
				},
				Constraints: constraintsRequest{
					PublicProfile: &publicProfileRequest{},
					StreamType:    &streamTypeRequest{Value: "live"},
				},
			},
		}
		output, err := xml.MarshalIndent(&reqData, "", "    ")
		CheckErr(err)
		reqString := fmt.Sprintf("%s%s\n", xml.Header, string(output))
		req, err := http.NewRequest("POST", endpoint, strings.NewReader(reqString))
		CheckErr(err)
		for _, h := range c.Headers {
			req.Header.Set(h[0], h[1])
		}
		req.Header.Set("Content-Type", "text/xml")
		_, buf, err := onlineRequest(req, client)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot send a query, %v", err)
		}
		decoder := xml.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
		parsed := &streamateResponse{}
		err = decoder.Decode(parsed)
		if err != nil {
			if c.Dbg {
				Ldbg("response: %s", buf.String())
			}
			return nil, nil, fmt.Errorf("[%v] cannot parse response %v", client.Addr, err)
		}
		for _, m := range parsed.AvailablePerformers.Performers {
			image := ""
			if m.Media != nil && m.Media.Pic != nil && m.Media.Pic.Full != nil {
				image = "https:" + m.Media.Pic.Full.Src
			}
			modelID := strings.ToLower(m.Name)
			onlineModels[modelID] = StatusOnline
			images[modelID] = image
		}
		if parsed.AvailablePerformers.ExactMatches != page {
			break
		}
	}
	return
}

// CheckStatusesMany returns Streamate online models
func (c *StreamateChecker) CheckStatusesMany(QueryModelList, CheckMode) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	return checkEndpoints(c, c.UsersOnlineEndpoints, c.Dbg)
}

// Start starts a daemon
func (c *StreamateChecker) Start()                 { c.startFullCheckerDaemon(c) }
func (c *StreamateChecker) createUpdater() Updater { return c.createFullUpdater(c) }
