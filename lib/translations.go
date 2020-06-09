package lib

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"text/template"

	"gopkg.in/yaml.v3"
)

//go:generate yamlenums -type=ParseKind
type ParseKind int

const (
	ParseRaw ParseKind = iota
	ParseHTML
	ParseMarkdown
)

func (r ParseKind) String() string {
	switch r {
	case ParseRaw:
		return "raw"
	case ParseHTML:
		return "html"
	case ParseMarkdown:
		return "markdown"
	}
	return "unknown"
}

type Translation struct {
	Key            string    `yaml:"-"`
	Str            string    `yaml:"str"`
	Parse          ParseKind `yaml:"parse"`
	DisablePreview bool      `yaml:"disable_preview"`
}

type AllTranslations map[string]*Translation

type Translations struct {
	Help                   *Translation `yaml:"help"`
	Online                 *Translation `yaml:"online"`
	List                   *Translation `yaml:"list"`
	Offline                *Translation `yaml:"offline"`
	Denied                 *Translation `yaml:"denied"`
	SyntaxAdd              *Translation `yaml:"syntax_add"`
	SyntaxRemove           *Translation `yaml:"syntax_remove"`
	SyntaxFeedback         *Translation `yaml:"syntax_feedback"`
	InvalidSymbols         *Translation `yaml:"invalid_symbols"`
	AlreadyAdded           *Translation `yaml:"already_added"`
	AddError               *Translation `yaml:"add_error"`
	ModelAdded             *Translation `yaml:"model_added"`
	ModelNotInList         *Translation `yaml:"model_not_in_list"`
	ModelRemoved           *Translation `yaml:"model_removed"`
	Feedback               *Translation `yaml:"feedback"`
	Social                 *Translation `yaml:"social"`
	UnknownCommand         *Translation `yaml:"unknown_command"`
	InvalidCommand         *Translation `yaml:"invalid_command"`
	Languages              *Translation `yaml:"languages"`
	Version                *Translation `yaml:"version"`
	ProfileRemoved         *Translation `yaml:"profile_removed"`
	NoOnlineModels         *Translation `yaml:"no_online_models"`
	RemoveAll              *Translation `yaml:"remove_all"`
	AllModelsRemoved       *Translation `yaml:"all_models_removed"`
	TryToBuyLater          *Translation `yaml:"try_to_buy_later"`
	PayThis                *Translation `yaml:"pay_this"`
	SelectCurrency         *Translation `yaml:"select_currency"`
	UnknownCurrency        *Translation `yaml:"unknown_currency"`
	BuyAd                  *Translation `yaml:"buy_ad"`
	YourMaxModels          *Translation `yaml:"your_max_models"`
	PaymentComplete        *Translation `yaml:"payment_complete"`
	MailReceived           *Translation `yaml:"mail_received"`
	BuyButton              *Translation `yaml:"buy_button"`
	ReferralLink           *Translation `yaml:"referral_link"`
	InvalidReferralLink    *Translation `yaml:"invalid_referral_link"`
	FollowerExists         *Translation `yaml:"follower_exists"`
	ReferralApplied        *Translation `yaml:"referral_applied"`
	OwnReferralLinkHit     *Translation `yaml:"own_referral_link_hit"`
	SubscriptionUsage      *Translation `yaml:"subscription_usage"`
	SubscriptionUsageAd    *Translation `yaml:"subscription_usage_ad"`
	NotEnoughSubscriptions *Translation `yaml:"not_enough_subscriptions"`
	Week                   *Translation `yaml:"week"`
	ZeroSubscriptions      *Translation `yaml:"zero_subscriptions"`
}

func LoadEndpointTranslations(files []string) (*Translations, AllTranslations) {
	tr := &Translations{}
	allTr := AllTranslations{}
	for _, t := range files {
		parsed := loadTranslations(t)
		for k, v := range parsed {
			v.Key = k
			allTr[k] = v
		}
	}
	copy(allTr, tr)
	CheckErr(noNils(tr))
	return tr, allTr
}

func setupTemplates(trs AllTranslations) *template.Template {
	tpl := template.New("")
	tpl.Funcs(template.FuncMap{"mod": func(i, j int) int { return i % j }})
	tpl.Funcs(template.FuncMap{"add": func(i, j int) int { return i + j }})
	for k, v := range trs {
		template.Must(tpl.New(k).Parse(v.Str))
	}

	return tpl
}

func LoadAllTranslations(files map[string][]string) (trans map[string]*Translations, tpl map[string]*template.Template) {
	trans = make(map[string]*Translations)
	tpl = make(map[string]*template.Template)
	for e, x := range files {
		tr, allTr := LoadEndpointTranslations(x)
		trans[e] = tr
		tpl[e] = setupTemplates(allTr)
	}
	return
}

func copy(from AllTranslations, to *Translations) {
	value := reflect.ValueOf(to).Elem()
	toType := reflect.TypeOf(to).Elem()
	for k, v := range from {
		for i := 0; i < value.NumField(); i++ {
			tag := toType.Field(i).Tag.Get("yaml")
			if tag == k {
				f := value.Field(i)
				f.Set(reflect.ValueOf(v))
				continue
			}
		}
	}
}

func noNils(x *Translations) error {
	rv := reflect.ValueOf(x).Elem()
	for i := 0; i < rv.NumField(); i++ {
		field := rv.Field(i)
		if field.IsNil() {
			tag := rv.Type().Field(i).Tag.Get("yaml")
			return fmt.Errorf("required translation is not set: %s", tag)
		}
	}
	return nil
}

func loadTranslations(path string) AllTranslations {
	file, err := os.Open(filepath.Clean(path))
	CheckErr(err)
	defer func() { CheckErr(file.Close()) }()
	decoder := yaml.NewDecoder(file)
	parsed := AllTranslations{}
	err = decoder.Decode(&parsed)
	CheckErr(err)
	return parsed
}
