package lib

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
)

type DescriptionsRequest struct {
	XMLName xml.Name `xml:"Descriptions"`
}

type MediaRequest struct {
	XMLName xml.Name `xml:"Media"`
	Value   string   `xml:",chardata"`
}

type StaticSortRequest struct {
	XMLName xml.Name `xml:"StaticSort"`
}

type IncludeRequest struct {
	XMLName      xml.Name `xml:"Include"`
	Descriptions *DescriptionsRequest
	Media        *MediaRequest
	StaticSort   *StaticSortRequest
}

type PublicProfileRequest struct {
	XMLName xml.Name `xml:"PublicProfile"`
}

type NameRequest struct {
	XMLName xml.Name `xml:"Name"`
	Value   string   `xml:",chardata"`
}

type StreamTypeRequest struct {
	XMLName xml.Name `xml:"StreamType"`
	Value   string   `xml:",chardata"`
}

type ConstraintsRequest struct {
	XMLName       xml.Name `xml:"Constraints"`
	PublicProfile *PublicProfileRequest
	Name          *NameRequest
	StreamType    *StreamTypeRequest
}

type AvailablePerformersRequest struct {
	XMLName           xml.Name `xml:"AvailablePerformers"`
	Exact             bool     `xml:"Exact,attr"`
	PageNum           int      `xml:"PageNum,attr"`
	CountTotalResults bool     `xml:"CountTotalResults,attr"`
	Include           IncludeRequest
	Constraints       ConstraintsRequest
}

type OptionsRequest struct {
	XMLName    xml.Name `xml:"Options"`
	MaxResults int      `xml:"MaxResults,attr"`
}

type StreamateRequest struct {
	XMLName             xml.Name `xml:"SMLQuery"`
	Options             OptionsRequest
	AvailablePerformers AvailablePerformersRequest
}

type FullResponse struct {
	XMLName xml.Name `xml:"Full"`
	Src     string   `xml:"Src,attr"`
}

type PicResponse struct {
	XMLName xml.Name      `xml:"Pic"`
	Full    *FullResponse `xml:"Full"`
}

type MediaResponse struct {
	XMLName xml.Name     `xml:"Media"`
	Pic     *PicResponse `xml:"Pic"`
}

type PerformerResponse struct {
	XMLName    xml.Name       `xml:"Performer"`
	Name       string         `xml:"Name,attr"`
	StreamType string         `xml:"StreamType,attr"`
	Media      *MediaResponse `xml:"Media"`
}

type AvailablePerformersResponse struct {
	XMLName          xml.Name            `xml:"AvailablePerformers"`
	ExactMatches     int                 `xml:"ExactMatches,attr"`
	TotalResultCount int                 `xml:"TotalResultCount,attr"`
	Performers       []PerformerResponse `xml:"Performer"`
}

type StreamateResponse struct {
	XMLName             xml.Name `xml:"SMLResult"`
	AvailablePerformers AvailablePerformersResponse
}

// CheckModelStreamate checks Streamate model status
func CheckModelStreamate(client *Client, modelID string, headers [][2]string, dbg bool, _ map[string]string) StatusKind {
	reqData := StreamateRequest{
		Options: OptionsRequest{MaxResults: 1},
		AvailablePerformers: AvailablePerformersRequest{
			Exact:   true,
			PageNum: 1,
			Constraints: ConstraintsRequest{
				PublicProfile: &PublicProfileRequest{},
				StreamType:    &StreamTypeRequest{Value: "live,recorded,offline"},
				Name:          &NameRequest{Value: modelID},
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
	parsed := &StreamateResponse{}
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
		reqData := StreamateRequest{
			Options: OptionsRequest{MaxResults: page},
			AvailablePerformers: AvailablePerformersRequest{
				Exact:             true,
				PageNum:           i,
				CountTotalResults: true,
				Include: IncludeRequest{
					Media: &MediaRequest{Value: "biopic"},
				},
				Constraints: ConstraintsRequest{
					PublicProfile: &PublicProfileRequest{},
					StreamType:    &StreamTypeRequest{Value: "live"},
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
		parsed := &StreamateResponse{}
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