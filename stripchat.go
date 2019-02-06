package siren

import (
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/net/html"
)

// CheckModelStripchat checks Stripchat model status
func CheckModelStripchat(client *http.Client, modelID string, dbg bool) StatusKind {
	resp, err := client.Get(fmt.Sprintf("https://stripchat.com/%s", modelID))
	if err != nil {
		Lerr("cannot send a query, %v", err)
		return StatusUnknown
	}
	defer func() {
		CheckErr(resp.Body.Close())
	}()
	if resp.StatusCode == 404 {
		return StatusNotFound
	}
	doc, err := html.Parse(resp.Body)
	if err != nil {
		Linf("cannot parse body for model %s, %v", modelID, err)
		return StatusUnknown
	}

	if findOfflineDiv(doc) != nil {
		return StatusOffline
	}

	return StatusOnline
}

func findOfflineDiv(node *html.Node) *html.Node {
	if node.Type == html.ElementNode && node.Data == "div" {
		for _, a := range node.Attr {
			if a.Key == "class" {
				cs := strings.Split(a.Val, " ")
				for _, c := range cs {
					if c == "status-off" {
						return node
					}
				}
			}
		}
	}
	for c := node.FirstChild; c != nil; c = c.NextSibling {
		if n := findOfflineDiv(c); n != nil {
			return n
		}
	}
	return nil
}
