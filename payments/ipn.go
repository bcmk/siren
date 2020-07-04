package payments

import (
	"bytes"
	"errors"
	"io/ioutil"
	"net/http"
	"strconv"

	"github.com/bcmk/siren/lib"
)

var ldbg = lib.Ldbg

// ParseIPN parses IPN request from CoinPayments
func ParseIPN(r *http.Request, ipnSecret string, debug bool) (StatusKind, string, error) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return StatusUnknown, "", errors.New("cannot read body")
	}
	lib.CheckErr(r.Body.Close())
	r.Body = ioutil.NopCloser(bytes.NewBuffer(body))

	if debug {
		ldbg("IPN headers: %s", r.Header)
		ldbg("IPN body: %s", string(body))
	}

	if err = r.ParseForm(); err != nil {
		return StatusUnknown, "", errors.New("cannot parse IPN data")
	}

	if r.Form.Get("ipn_mode") != "hmac" {
		return StatusUnknown, "", errors.New("IPN mode in POST data is not HMAC")
	}

	httpHMAC := r.Header.Get("HMAC")
	if httpHMAC == "" {
		return StatusUnknown, "", errors.New("HMAC header is not set")
	}

	hash := calcHMAC(string(body), ipnSecret)
	if httpHMAC != hash {
		return StatusUnknown, "", errors.New("signatures don't match")
	}

	if r.Form.Get("merchant") == "" {
		return StatusUnknown, "", errors.New("merchant is not set in POST data")
	}

	status, err := strconv.ParseInt(r.Form.Get("status"), 10, 32)
	if err != nil {
		return StatusUnknown, "", errors.New("cannot parse status from POST data")
	}

	custom := r.Form.Get("custom")
	if custom == "" {
		return StatusUnknown, "", errors.New("no transaction UUID in POST data")
	}

	if status >= 100 || status == 2 {
		return StatusFinished, custom, nil
	} else if status < 0 {
		return StatusCanceled, custom, nil
	}

	return StatusCreated, custom, nil
}
