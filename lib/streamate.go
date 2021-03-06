package lib

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

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

// CheckModelStreamate checks Streamate model status
func CheckModelStreamate(client *Client, modelID string, headers [][2]string, dbg bool, _ map[string]string) StatusKind {
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
	for _, h := range headers {
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
	if dbg {
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
		if dbg {
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

// StreamateOnlineAPI returns Streamate online models
func StreamateOnlineAPI(
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
		for _, h := range headers {
			req.Header.Set(h[0], h[1])
		}
		req.Header.Set("Content-Type", "text/xml")
		_, buf, err := onlineRequest(req, client)
		if err != nil {
			return nil, fmt.Errorf("cannot send a query, %v", err)
		}
		decoder := xml.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
		parsed := &streamateResponse{}
		err = decoder.Decode(parsed)
		if err != nil {
			if dbg {
				Ldbg("response: %s", buf.String())
			}
			return nil, fmt.Errorf("[%v] cannot parse response %v", client.Addr, err)
		}
		for _, m := range parsed.AvailablePerformers.Performers {
			image := ""
			if m.Media != nil && m.Media.Pic != nil && m.Media.Pic.Full != nil {
				image = "https:" + m.Media.Pic.Full.Src
			}
			onlineModels[m.Name] = OnlineModel{ModelID: m.Name, Image: image}
		}
		if parsed.AvailablePerformers.ExactMatches != page {
			break
		}
	}
	return
}

// StartStreamateChecker starts a checker for Chaturbate
func StartStreamateChecker(
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
	return StartChecker(CheckModelStreamate, StreamateOnlineAPI, usersOnlineEndpoint, clients, headers, intervalMs, dbg, specificConfig)
}
