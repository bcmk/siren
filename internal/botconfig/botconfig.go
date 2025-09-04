// Package botconfig represents bot configuration
package botconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bcmk/siren/lib/cmdlib"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var checkErr = cmdlib.CheckErr

type endpoint struct {
	ListenPath          string   `mapstructure:"listen_path"`          // the path excluding domain to listen to, the good choice is "/your-telegram-bot-token"
	StatPath            string   `mapstructure:"stat_path"`            // the path for statistics
	WebhookDomain       string   `mapstructure:"webhook_domain"`       // the domain listening to the webhook
	CertificatePath     string   `mapstructure:"certificate_path"`     // a path to your certificate, it is used to set up a webhook and to set up this HTTP server
	BotToken            string   `mapstructure:"bot_token"`            // your Telegram bot token
	Translation         []string `mapstructure:"translation"`          // translation files
	Ads                 []string `mapstructure:"ads"`                  // ads files
	Images              string   `mapstructure:"images"`               // images directory
	MaintenanceResponse string   `mapstructure:"maintenance_response"` // the maintenance response
}

// StatusConfirmationSeconds represents a configureation of confirmation durations for each of specific statuses
type StatusConfirmationSeconds struct {
	Offline  int `mapstructure:"offline"`
	Online   int `mapstructure:"online"`
	NotFound int `mapstructure:"not_found"`
	Denied   int `mapstructure:"denied"`
}

// Config represents bot configuration
type Config struct {
	Debug                           bool                      `mapstructure:"debug"`                              // debug mode
	CheckGID                        bool                      `mapstructure:"check_gid"`                          // check goroutines ids
	ListenAddress                   string                    `mapstructure:"listen_address"`                     // the address to listen to
	Website                         string                    `mapstructure:"website"`                            // one of the following strings: "bongacams", "stripchat", "chaturbate", "livejasmin", "flirt4free", "streamate", "cam4"
	WebsiteLink                     string                    `mapstructure:"website_link"`                       // affiliate link to website
	PeriodSeconds                   int                       `mapstructure:"period_seconds"`                     // the period of querying models statuses
	CleaningPeriodSeconds           int                       `mapstructure:"cleaning_period_seconds"`            // the cleaning period
	MaintainDBPeriodSeconds         int                       `mapstructure:"maintain_db_period_seconds"`         // the maintain DB period
	MaxModels                       int                       `mapstructure:"max_models"`                         // maximum models per user
	TimeoutSeconds                  int                       `mapstructure:"timeout_seconds"`                    // HTTP timeout
	AdminID                         int64                     `mapstructure:"admin_id"`                           // admin Telegram ID
	AdminEndpoint                   string                    `mapstructure:"admin_endpoint"`                     // admin endpoint
	DBPath                          string                    `mapstructure:"db_path"`                            // path to the database
	BlockThreshold                  int                       `mapstructure:"block_threshold"`                    // do not send a message to the user after being blocked by him this number of times
	IntervalMs                      int                       `mapstructure:"interval_ms"`                        // queries interval per IP address for rate limited access
	SourceIPAddresses               []string                  `mapstructure:"source_ip_addresses"`                // source IP addresses for rate limited access
	DangerousErrorRate              string                    `mapstructure:"dangerous_error_rate"`               // dangerous error rate, warn admin if it is reached, format "1000/10000"
	EnableCookies                   bool                      `mapstructure:"enable_cookies"`                     // enable cookies, it can be useful to mitigate rate limits
	Headers                         [][2]string               `mapstructure:"headers"`                            // HTTP headers to make queries with
	StatPassword                    string                    `mapstructure:"stat_password"`                      // password for statistics
	ErrorReportingPeriodMinutes     int                       `mapstructure:"error_reporting_period_minutes"`     // the period of the error reports
	Endpoints                       map[string]endpoint       `mapstructure:"endpoints"`                          // the endpoints by simple name, used for the support of the bots in different languages accessing the same database
	HeavyUserRemainder              int                       `mapstructure:"heavy_user_remainder"`               // the maximum remainder of models to treat a user as heavy
	ReferralBonus                   int                       `mapstructure:"referral_bonus"`                     // number of additional subscriptions for a referrer
	FollowerBonus                   int                       `mapstructure:"follower_bonus"`                     // number of additional subscriptions for a new user registered by a referral link
	UsersOnlineEndpoint             []string                  `mapstructure:"users_online_endpoint"`              // the endpoint to fetch online users
	StatusConfirmationSeconds       StatusConfirmationSeconds `mapstructure:"status_confirmation_seconds"`        // a status is confirmed only if it lasts for at least this number of seconds
	OfflineNotifications            bool                      `mapstructure:"offline_notifications"`              // enable offline notifications
	SQLPrelude                      []string                  `mapstructure:"sql_prelude"`                        // run these SQL commands before any other
	EnableWeek                      bool                      `mapstructure:"enable_week"`                        // enable week command
	AffiliateLink                   string                    `mapstructure:"affiliate_link"`                     // affiliate link template
	SpecificConfig                  map[string]string         `mapstructure:"specific_config"`                    // the config for specific website
	TelegramTimeoutSeconds          int                       `mapstructure:"telegram_timeout_seconds"`           // the timeout for Telegram queries
	MaxSubscriptionsForPics         int                       `mapstructure:"max_subscriptions_for_pics"`         // the maximum amount of subscriptions for pics in a group chat
	KeepStatusesForDays             int                       `mapstructure:"keep_statuses_for_days"`             // keep statuses for this number of days
	MaxCleanSeconds                 int                       `mapstructure:"max_clean_seconds"`                  // maximum number of seconds to clean
	SubsConfirmationPeriodSeconds   int                       `mapstructure:"subs_confirmation_period_seconds"`   // subscriptions confirmation period
	NotificationsReadyPeriodSeconds int                       `mapstructure:"notifications_ready_period_seconds"` // notifications ready check period
	SpecialModels                   bool                      `mapstructure:"special_models"`                     // process special models
	ShowImages                      bool                      `mapstructure:"show_images"`                        // images support

	ErrorThreshold   int
	ErrorDenominator int
}

var fractionRegexp = regexp.MustCompile(`^(\d+)/(\d+)$`)

type configFile struct {
	name     string
	required bool
}

func bindEnvForStructType(v *viper.Viper, t reflect.Type, prefix string, bindPrimitiveMaps bool) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			tag := f.Tag.Get("mapstructure")
			if tag == "" || tag == "-" {
				continue
			}
			key := tag
			if prefix != "" {
				key = prefix + "." + tag
			}
			bindEnvForStructType(v, f.Type, key, bindPrimitiveMaps)
		}
	case reflect.Map:
		if !bindPrimitiveMaps {
			return
		}
		k, e := t.Key(), t.Elem()
		for e.Kind() == reflect.Ptr {
			e = e.Elem()
		}
		if k.Kind() == reflect.String && isPrimitiveKind(e.Kind()) {
			_ = v.BindEnv(prefix)
		}
	default:
		_ = v.BindEnv(prefix)
	}
}

func isPrimitiveKind(k reflect.Kind) bool {
	switch k {
	case
		reflect.Bool,
		reflect.Int,
		reflect.Int8,
		reflect.Int16,
		reflect.Int32,
		reflect.Int64,
		reflect.Uint,
		reflect.Uint8,
		reflect.Uint16,
		reflect.Uint32,
		reflect.Uint64,
		reflect.Uintptr,
		reflect.Float32,
		reflect.Float64,
		reflect.String:

		return true
	default:
		return false
	}
}

func stringToMapHookFunc() mapstructure.DecodeHookFunc {
	return func(from, to reflect.Type, data any) (any, error) {
		if from.Kind() == reflect.String && to.Kind() == reflect.Map {
			if s := data.(string); s != "" {
				m := reflect.New(to).Interface()
				if err := json.Unmarshal([]byte(s), m); err != nil {
					return data, err
				}
				return reflect.ValueOf(m).Elem().Interface(), nil
			}
		}
		return data, nil
	}
}

func stringToSliceHookFunc(sep string) mapstructure.DecodeHookFunc {
	return func(
		f reflect.Type,
		t reflect.Type,
		data interface{},
	) (interface{}, error) {
		if f.Kind() != reflect.String {
			return data, nil
		}
		if t != reflect.SliceOf(f) {
			return data, nil
		}

		raw := data.(string)
		if raw == "" {
			return []string{}, nil
		}

		result := strings.Split(raw, sep)
		for k, v := range result {
			result[k] = strings.TrimLeft(v, " ")
		}
		return result, nil
	}
}

var cfgPath = pflag.StringP("config", "c", "", "path to a config file (overrides default search)")

// ReadConfig reads config
func ReadConfig() *Config {
	pflag.Parse()

	var configFiles []configFile
	if *cfgPath != "" {
		configFiles = []configFile{{*cfgPath, true}}
	} else {
		configFiles = []configFile{
			{"config.json", true},
			{"config.dev.ignore.json", false},
		}
	}

	v := viper.New()
	v.SetConfigType("json")

	for _, f := range configFiles {
		v.SetConfigFile(f.name)
		if err := v.MergeInConfig(); err != nil {
			if errors.Is(err, fs.ErrNotExist) && !f.required {
				log.Printf("skip config %q", f.name)
				continue
			}
			log.Fatalf("error reading %q: %v", f.name, err)
		}
		log.Printf("successfully read config %q", f.name)
	}

	v.SetEnvPrefix("XRN")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	cfg := &Config{ShowImages: true}
	bindEnvForStructType(v, reflect.TypeOf(cfg), "", false)
	checkErr(v.Unmarshal(&cfg, func(dc *mapstructure.DecoderConfig) {
		dc.ErrorUnused = true
		dc.DecodeHook = mapstructure.ComposeDecodeHookFunc(
			stringToSliceHookFunc(","),
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToTimeHookFunc(time.RFC3339),
			mapstructure.TextUnmarshallerHookFunc(),
			stringToMapHookFunc(),
		)
	}))
	checkErr(checkConfig(cfg))
	if len(cfg.SourceIPAddresses) == 0 {
		cfg.SourceIPAddresses = append(cfg.SourceIPAddresses, "")
	}

	return cfg
}

func checkConfig(cfg *Config) error {
	for _, x := range cfg.SourceIPAddresses {
		if net.ParseIP(x) == nil {
			return fmt.Errorf("cannot parse sourece IP address %s", x)
		}
	}
	for _, x := range cfg.Endpoints {
		if x.ListenPath == "" {
			return errors.New("configure listen_path")
		}
		if x.BotToken == "" {
			return errors.New("configure bot_token")
		}
		if len(x.Translation) == 0 {
			return errors.New("configure translation")
		}
		if x.Images == "" {
			return errors.New("configure images")
		}
		if x.MaintenanceResponse == "" {
			return errors.New("configure maintenance_response")
		}
	}
	if cfg.ListenAddress == "" {
		return errors.New("configure listen_address")
	}
	if _, found := cfg.Endpoints[cfg.AdminEndpoint]; !found {
		return errors.New("configure admin_endpoint")
	}
	if cfg.PeriodSeconds == 0 {
		return errors.New("configure period_seconds")
	}
	if cfg.MaxModels == 0 {
		return errors.New("configure max_models")
	}
	if cfg.TimeoutSeconds == 0 {
		return errors.New("configure timeout_seconds")
	}
	if cfg.AdminID == 0 {
		return errors.New("configure admin_id")
	}
	if cfg.DBPath == "" {
		return errors.New("configure db_path")
	}
	if cfg.BlockThreshold == 0 {
		return errors.New("configure block_threshold")
	}
	if cfg.Website == "" {
		return errors.New("configure website")
	}
	if cfg.WebsiteLink == "" {
		return errors.New("configure website_link")
	}
	if cfg.Website == "livejasmin" {
		if cfg.SpecificConfig["ps_id"] == "" {
			return errors.New("configure specific_config/ps_id")
		}
		if cfg.SpecificConfig["access_key"] == "" {
			return errors.New("configure specific_config/access_key")
		}
	}
	if cfg.StatPassword == "" {
		return errors.New("configure stat_password")
	}
	if cfg.ErrorReportingPeriodMinutes == 0 {
		return errors.New("configure error_reporting_period_minutes")
	}
	if cfg.HeavyUserRemainder == 0 {
		return errors.New("configure heavy_user_remainder")
	}
	if cfg.ReferralBonus == 0 {
		return errors.New("configure referral_bonus")
	}
	if cfg.FollowerBonus == 0 {
		return errors.New("configure follower_bonus")
	}
	if cfg.AffiliateLink == "" {
		cfg.AffiliateLink = "{{ . }}"
	}
	if cfg.TelegramTimeoutSeconds == 0 {
		return errors.New("configure telegram_timeout_seconds")
	}
	if cfg.MaxSubscriptionsForPics == 0 {
		return errors.New("configure max_subscriptions_for_pics")
	}
	if cfg.KeepStatusesForDays == 0 {
		return errors.New("configure keep_statuses_for_days")
	}
	if cfg.SubsConfirmationPeriodSeconds == 0 {
		return errors.New("configure subs_confirmation_period_seconds")
	}
	if cfg.NotificationsReadyPeriodSeconds == 0 {
		return errors.New("configure notifications_ready_period_seconds")
	}

	if m := fractionRegexp.FindStringSubmatch(cfg.DangerousErrorRate); len(m) == 3 {
		errorThreshold, err := strconv.ParseInt(m[1], 10, 0)
		if err != nil {
			return err
		}

		errorDenominator, err := strconv.ParseInt(m[2], 10, 0)
		if err != nil {
			return err
		}

		if errorDenominator == 0 {
			return errors.New(`configure dangerous_errors_rate as "x/y", where y > 0`)
		}

		cfg.ErrorThreshold = int(errorThreshold)
		cfg.ErrorDenominator = int(errorDenominator)
	} else {
		return errors.New("configure dangerous_error_rate")
	}

	return nil
}
