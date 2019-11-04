package lib

import (
	"net"
	"net/http"
	"net/http/cookiejar"
	"time"
)

// NoRedirect tells HTTP client to not to redirect
func NoRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

// HTTPClientWithTimeoutAndAddress returns HTTP client bound to specific IP address
func HTTPClientWithTimeoutAndAddress(timeoutSeconds int, address string, cookies bool) *http.Client {
	var client = &http.Client{
		CheckRedirect: NoRedirect,
		Timeout:       time.Second * time.Duration(timeoutSeconds),
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				LocalAddr: &net.TCPAddr{IP: net.ParseIP(address)},
				Timeout:   time.Second * time.Duration(timeoutSeconds),
				KeepAlive: time.Second * time.Duration(timeoutSeconds),
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          10,
			IdleConnTimeout:       time.Second * time.Duration(timeoutSeconds),
			TLSHandshakeTimeout:   time.Second * time.Duration(timeoutSeconds),
			ExpectContinueTimeout: time.Second * time.Duration(timeoutSeconds),
		},
	}
	if cookies {
		cookieJar, _ := cookiejar.New(nil)
		client.Jar = cookieJar
	}
	return client
}
