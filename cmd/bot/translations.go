package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

//go:generate jsonenums -type=parseKind
type parseKind int

const (
	parseRaw parseKind = iota
	parseHTML
	parseMarkdown
)

func (r parseKind) String() string {
	switch r {
	case parseRaw:
		return "raw"
	case parseHTML:
		return "html"
	case parseMarkdown:
		return "markdown"
	}
	return "unknown"
}

type translation struct {
	Str   string    `json:"str"`
	Parse parseKind `json:"parse"`
}

type translations struct {
	Help                   *translation `json:"help"`
	Online                 *translation `json:"online"`
	OnlineList             *translation `json:"online_list"`
	Offline                *translation `json:"offline"`
	OfflineList            *translation `json:"offline_list"`
	Denied                 *translation `json:"denied"`
	DeniedList             *translation `json:"denied_list"`
	SyntaxAdd              *translation `json:"syntax_add"`
	SyntaxRemove           *translation `json:"syntax_remove"`
	SyntaxFeedback         *translation `json:"syntax_feedback"`
	InvalidSymbols         *translation `json:"invalid_symbols"`
	AlreadyAdded           *translation `json:"already_added"`
	AddError               *translation `json:"add_error"`
	ModelAdded             *translation `json:"model_added"`
	ModelNotInList         *translation `json:"model_not_in_list"`
	ModelRemoved           *translation `json:"model_removed"`
	Donation               *translation `json:"donation"`
	Feedback               *translation `json:"feedback"`
	SourceCode             *translation `json:"source_code"`
	UnknownCommand         *translation `json:"unknown_command"`
	InvalidCommand         *translation `json:"invalid_command"`
	Slash                  *translation `json:"slash"`
	Languages              *translation `json:"languages"`
	Version                *translation `json:"version"`
	ProfileRemoved         *translation `json:"profile_removed"`
	NoModels               *translation `json:"no_models"`
	NoOnlineModels         *translation `json:"no_online_models"`
	RemoveAll              *translation `json:"remove_all"`
	AllModelsRemoved       *translation `json:"all_models_removed"`
	TryToBuyLater          *translation `json:"try_to_buy_later"`
	PayThis                *translation `json:"pay_this"`
	SelectCurrency         *translation `json:"select_currency"`
	UnknownCurrency        *translation `json:"unknown_currency"`
	BuyAd                  *translation `json:"buy_ad"`
	YourMaxModels          *translation `json:"your_max_models"`
	PaymentComplete        *translation `json:"payment_complete"`
	MailReceived           *translation `json:"mail_received"`
	BuyButton              *translation `json:"buy_button"`
	ReferralLink           *translation `json:"referral_link"`
	OwnReferralLinkHit     *translation `json:"own_referral_link_hit"`
	SubscriptionUsage      *translation `json:"subscription_usage"`
	SubscriptionUsageAd    *translation `json:"subscription_usage_ad"`
	NotEnoughSubscriptions *translation `json:"not_enough_subscriptions"`
}

func loadAllTranslations(cfg *config) map[string]translations {
	result := make(map[string]translations)
	for e, x := range cfg.Endpoints {
		result[e] = loadTranslations(x.Translation)
	}
	return result
}

func noNils(x interface{}) error {
	rv := reflect.ValueOf(x)
	for i := 0; i < rv.NumField(); i++ {
		field := rv.Field(i)
		if field.IsNil() {
			tag := rv.Type().Field(i).Tag.Get("json")
			return fmt.Errorf("required translation is not set: %s", tag)
		}
	}
	return nil
}

func loadTranslations(path string) translations {
	file, err := os.Open(filepath.Clean(path))
	checkErr(err)
	defer func() { checkErr(file.Close()) }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	parsed := translations{}
	err = decoder.Decode(&parsed)
	checkErr(err)
	checkErr(noNils(parsed))
	return parsed
}
