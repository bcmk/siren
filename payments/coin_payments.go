package payments

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/bcmk/siren/lib"
	"github.com/shopspring/decimal"
)

// CoinPaymentsAPI implements CoinPayments API
type CoinPaymentsAPI struct {
	nonce      int
	publicKey  string
	privateKey string
	httpClient *http.Client
	apiURL     string
	ipnURL     string
	debug      bool
}

// NewCoinPaymentsAPI returns new CoinPaymentsAPI object
func NewCoinPaymentsAPI(publicKey, privateKey, ipnURL string, timeoutSeconds int, debug bool) *CoinPaymentsAPI {
	return &CoinPaymentsAPI{
		nonce:      int(time.Now().Unix()),
		publicKey:  publicKey,
		privateKey: privateKey,
		httpClient: &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second},
		apiURL:     "https://www.coinpayments.net/api.php",
		ipnURL:     ipnURL,
		debug:      debug,
	}
}

type kv struct {
	k string
	v string
}

func calcHMAC(message, secret string) string {
	hash := hmac.New(sha512.New, []byte(secret))
	_, err := hash.Write([]byte(message))
	lib.CheckErr(err)
	return hex.EncodeToString(hash.Sum(nil))
}

func (api *CoinPaymentsAPI) coinpaymentsMethod(method string, additionalParams []kv) (body []byte, err error) {
	params := []kv{
		{"version", "1"},
		{"cmd", method},
		{"key", api.publicKey},
		{"nonce", strconv.Itoa(api.nonce)},
		{"format", "json"}}
	params = append(params, additionalParams...)

	values := url.Values{}
	for _, i := range params {
		values.Set(i.k, i.v)
	}

	payload := values.Encode()
	req, err := http.NewRequest("POST", api.apiURL, bytes.NewBuffer([]byte(payload)))

	if err != nil {
		return nil, fmt.Errorf("cannot create request: %v", err)
	}

	api.nonce++

	hash := calcHMAC(payload, api.privateKey)
	req.Header.Set("HMAC", hash)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if api.debug {
		lib.Ldbg("creating transaction: %s", payload)
		lib.Ldbg("headers: %v", req.Header)
	}

	res, err := api.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot perform request: %w", err)
	}
	defer lib.CheckErr(res.Body.Close())

	body, err = ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("cannot read body: %w", err)
	}

	if api.debug {
		lib.Ldbg("got response: %s", string(body))
	}

	return
}

// Transaction represents CoinPayments transaction
type Transaction struct {
	Amount         decimal.Decimal `json:"amount"`
	Address        string          `json:"address"`
	DestTag        flex            `json:"dest_tag"`
	TXNID          string          `json:"txn_id"`
	ConfirmsNeeded string          `json:"confirms_needed"`
	Timeout        uint32          `json:"timeout"`
	CheckoutURL    string          `json:"checkout_url"`
	StatusURL      string          `json:"status_url"`
	QRCodeURL      string          `json:"qrcode_url"`
}

type transactionResponse struct {
	Error  string             `json:"error"`
	Result transactionWrapper `json:"result"`
}

// CreateTransaction creates transaction object
func (api *CoinPaymentsAPI) CreateTransaction(amount int, currency string, email string, transactionUUID string) (res *Transaction, err error) {
	body, err := api.coinpaymentsMethod("create_transaction", []kv{
		{"amount", strconv.Itoa(amount)},
		{"currency1", "USD"},
		{"currency2", currency},
		{"buyer_email", email},
		{"ipn_url", api.ipnURL},
		{"custom", transactionUUID},
	})

	if err != nil {
		return
	}

	parse := &transactionResponse{}
	if err = json.Unmarshal(body, parse); err != nil {
		return nil, fmt.Errorf(`cannot unmarshal "%s", %w`, string(body), err)
	}
	if parse.Error != "ok" {
		return nil, errors.New(parse.Error)
	}
	if parse.Result.Transaction == nil {
		return nil, fmt.Errorf(`cannot unmarshal "%s", %w`, string(body), err)
	}
	res = parse.Result.Transaction
	return
}
