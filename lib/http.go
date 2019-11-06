package lib

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/cookiejar"
	"time"
)

// Client wraps HTTP client and source IP address
type Client struct {
	// Client is HTTP client
	Client *http.Client
	// Addr is source IP address
	Addr net.Addr
}

// NoRedirect tells HTTP client to not to redirect
func NoRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

// HTTPClientWithTimeoutAndAddress returns HTTP client bound to specific IP address
func HTTPClientWithTimeoutAndAddress(timeoutSeconds int, address string, cookies bool) *Client {
	addr := &net.TCPAddr{IP: net.ParseIP(address)}
	var client = &http.Client{
		CheckRedirect: NoRedirect,
		Timeout:       time.Second * time.Duration(timeoutSeconds),
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				LocalAddr: addr,
				Timeout:   time.Second * time.Duration(timeoutSeconds),
				KeepAlive: time.Second * time.Duration(timeoutSeconds),
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          10,
			IdleConnTimeout:       time.Second * time.Duration(timeoutSeconds),
			TLSHandshakeTimeout:   time.Second * time.Duration(timeoutSeconds),
			ExpectContinueTimeout: time.Second * time.Duration(timeoutSeconds),
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
	if cookies {
		cookieJar, _ := cookiejar.New(nil)
		client.Jar = cookieJar
	}
	return &Client{Client: client, Addr: addr}
}
