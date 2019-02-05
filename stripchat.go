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

func findOfflineDiv(doc *html.Node) *html.Node {
	var b *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if /*n.Type == html.ElementNode &&*/ n.Data == "div" {
			for _, a := range n.Attr {
				if a.Key == "class" {
					cs := strings.Split(a.Val, " ")
					for _, c := range cs {
						if c == "status-off" {
							b = n
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	return b
}
