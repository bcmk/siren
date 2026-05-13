package cmdlib

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// NoRedirect tells HTTP client not to redirect
func NoRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

// HTTPClientWithTimeout returns an HTTP client with the given timeout.
// A zero timeout disables the timeout (the same convention http.Client
// itself uses) and is intended for tests.
func HTTPClientWithTimeout(timeout time.Duration) *http.Client {
	return httpClient(timeout, http.ProxyFromEnvironment)
}

// HTTPClientWithProxy returns an HTTP client routing all requests
// through the given proxy URL (supports http, https, socks5 schemes).
func HTTPClientWithProxy(timeout time.Duration, proxyURL *url.URL) *http.Client {
	return httpClient(timeout, http.ProxyURL(proxyURL))
}

func httpClient(timeout time.Duration, proxy func(*http.Request) (*url.URL, error)) *http.Client {
	return &http.Client{
		CheckRedirect: NoRedirect,
		Timeout:       timeout,
		Transport: &http.Transport{
			Proxy: proxy,
			DialContext: (&net.Dialer{
				Timeout:   timeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       http.DefaultTransport.(*http.Transport).IdleConnTimeout,
			TLSHandshakeTimeout:   timeout,
			ExpectContinueTimeout: time.Duration(0),
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
}

// OnlineQuery creates and performs online request
func OnlineQuery(
	usersOnlineEndpoint string,
	client *http.Client,
	headers [][2]string,
) (
	*http.Response,
	*bytes.Buffer,
	error,
) {
	req, err := http.NewRequest("GET", usersOnlineEndpoint, nil)
	CheckErr(err)
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	return OnlineRequest(req, client)
}

// OnlineRequest performs online request
func OnlineRequest(
	req *http.Request,
	client *http.Client,
) (
	*http.Response,
	*bytes.Buffer,
	error,
) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("sending error, %w", err)
	}
	defer CloseBody(resp.Body)
	buf := bytes.Buffer{}
	_, err = buf.ReadFrom(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read response, %w", err)
	}
	return resp, &buf, nil
}
