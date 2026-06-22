package checkers

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bcmk/siren/v3/lib/cmdlib"
)

// StreamateChecker implements a checker for Streamate
type StreamateChecker struct {
	BaseChecker[*SimpleCheckerConfig]
}

var _ Checker = &StreamateChecker{}

// Site returns the site name.
func (*StreamateChecker) Site() string { return "streamate" }

// Init loads streamate-checker.json.
func (c *StreamateChecker) Init(checkerCfgPath string) error {
	if err := c.ensureUninitialised(); err != nil {
		return err
	}
	cfg := &SimpleCheckerConfig{}
	if err := readCheckerConfig(cfg, c.Site(), checkerCfgPath); err != nil {
		return err
	}
	c.BaseChecker = NewBaseChecker(cfg)
	return nil
}

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
	XMLName     xml.Name       `xml:"Performer"`
	Name        string         `xml:"Name,attr"`
	StreamType  string         `xml:"StreamType,attr"`
	PartyChat   string         `xml:"PartyChat,attr"`
	GoldShow    string         `xml:"GoldShow,attr"`
	PreGoldShow string         `xml:"PreGoldShow,attr"`
	Media       *mediaResponse `xml:"Media"`
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

func streamateShowKind(m performerResponse) cmdlib.ShowKind {
	if m.GoldShow == "1" {
		return cmdlib.ShowTicket
	}
	if m.PartyChat == "1" || m.PreGoldShow == "1" {
		return cmdlib.ShowPublic
	}
	return cmdlib.ShowUnknown
}

// QueryStatus checks Streamate model status
func (c *StreamateChecker) QueryStatus(modelID string) (cmdlib.StreamerInfoWithStatus, error) {
	reqData := streamateRequest{
		Options: optionsRequest{MaxResults: 1},
		AvailablePerformers: availablePerformersRequest{
			Exact:   true,
			PageNum: 1,
			Constraints: constraintsRequest{
				StreamType: &streamTypeRequest{Value: "live,recorded,offline"},
				Name:       &nameRequest{Value: modelID},
			},
		},
	}
	output, err := xml.MarshalIndent(&reqData, "", "    ")
	cmdlib.CheckErr(err)
	reqString := fmt.Sprintf("%s%s\n", xml.Header, string(output))
	req, err := http.NewRequest("POST", "https://affiliate.streamate.com/SMLive/SMLResult.xml", strings.NewReader(reqString))
	cmdlib.CheckErr(err)
	for _, h := range c.Cfg.Headers {
		req.Header.Set(h[0], h[1])
	}
	req.Header.Set("Content-Type", "text/xml")
	resp, err := c.Client.Do(req)
	if err != nil {
		cmdlib.Lerr("cannot send a query, %v", err)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	defer cmdlib.CloseBody(resp.Body)
	cmdlib.Ldbg("query status for %s: %d", modelID, resp.StatusCode)
	if resp.StatusCode == 404 {
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusNotFound}, nil
	}
	buf := bytes.Buffer{}
	_, err = buf.ReadFrom(resp.Body)
	if err != nil {
		cmdlib.Lerr("cannot read response for model %s, %v", modelID, err)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	decoder := xml.NewDecoder(io.NopCloser(bytes.NewReader(buf.Bytes())))
	parsed := &streamateResponse{}
	err = decoder.Decode(parsed)
	cmdlib.Ldbg("response: %s", buf.String())
	if err != nil {
		cmdlib.Lerr("cannot parse response for model %s, %v", modelID, err)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	if parsed.AvailablePerformers.ExactMatches != 1 || len(parsed.AvailablePerformers.Performers) != 1 {
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusNotFound}, nil
	}
	switch parsed.AvailablePerformers.Performers[0].StreamType {
	case "live":
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusOnline}, nil
	case "recorded", "offline":
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusOffline}, nil
	}
	return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
}

// QueryOnlineStreamers returns Streamate online models
func (c *StreamateChecker) QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error) {
	streamers := map[string]cmdlib.StreamerInfo{}
	endpoint := c.Cfg.UsersOnlineEndpoint
	// Somehow 500 doesn't work well
	queriedPageSize := 400
	pages := 1
	i := 0
	for i < pages {
		i++
		reqData := streamateRequest{
			Options: optionsRequest{MaxResults: queriedPageSize},
			AvailablePerformers: availablePerformersRequest{
				Exact:             true,
				PageNum:           i,
				CountTotalResults: i == 1,
				Include: includeRequest{
					Media:      &mediaRequest{Value: "biopic"},
					StaticSort: &staticSortRequest{},
				},
				Constraints: constraintsRequest{
					StreamType: &streamTypeRequest{Value: "live"},
				},
			},
		}
		output, err := xml.MarshalIndent(&reqData, "", "    ")
		cmdlib.CheckErr(err)
		reqString := fmt.Sprintf("%s%s\n", xml.Header, string(output))
		req, err := http.NewRequest("POST", endpoint, strings.NewReader(reqString))
		cmdlib.CheckErr(err)
		for _, h := range c.Cfg.Headers {
			req.Header.Set(h[0], h[1])
		}
		req.Header.Set("Content-Type", "text/xml")
		_, buf, err := cmdlib.OnlineRequest(req, c.Client)
		if err != nil {
			return nil, fmt.Errorf("cannot send a query, %v", err)
		}
		decoder := xml.NewDecoder(io.NopCloser(bytes.NewReader(buf.Bytes())))
		parsed := &streamateResponse{}
		err = decoder.Decode(parsed)
		cmdlib.Ldbg("response: %s", buf.String())
		if err != nil {
			return nil, fmt.Errorf("cannot parse response %v", err)
		}
		for _, m := range parsed.AvailablePerformers.Performers {
			image := ""
			if m.Media != nil && m.Media.Pic != nil && m.Media.Pic.Full != nil {
				image = "https:" + m.Media.Pic.Full.Src
			}
			modelID := strings.ToLower(m.Name)
			streamers[modelID] = cmdlib.StreamerInfo{
				ImageURL: image,
				ShowKind: streamateShowKind(m),
			}
		}
		if i == 1 {
			pages = (parsed.AvailablePerformers.TotalResultCount + queriedPageSize - 1) / queriedPageSize
			if pages > 20 {
				pages = 20
			}
		}
	}
	if len(streamers) == 0 {
		return nil, errors.New("zero online models reported")
	}
	return streamers, nil
}

// QueryFixedListOnlineStreamers is not implemented for online list checkers
func (c *StreamateChecker) QueryFixedListOnlineStreamers([]string, cmdlib.CheckMode) (map[string]cmdlib.StreamerInfo, error) {
	return nil, ErrNotImplemented
}

// Capabilities lists the surfaces Streamate exposes for dispatch.
func (*StreamateChecker) Capabilities() Capabilities {
	return Capabilities{
		SupportsQueryOnlineStreamers:          true,
		SupportsQueryFixedListOnlineStreamers: false,
		SupportsQueryFixedListStatuses:        false,
		SupportsQueryStatus:                   true,
		SupportsCLI:                           true,
	}
}
