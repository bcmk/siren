// Package botconfig represents bot configuration
package botconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/bcmk/siren/lib/cmdlib"
)

var checkErr = cmdlib.CheckErr

type endpoint struct {
	ListenPath          string   `json:"listen_path"`          // the path excluding domain to listen to, the good choice is "/your-telegram-bot-token"
	StatPath            string   `json:"stat_path"`            // the path for statistics
	WebhookDomain       string   `json:"webhook_domain"`       // the domain listening to the webhook
	CertificatePath     string   `json:"certificate_path"`     // a path to your certificate, it is used to set up a webhook and to set up this HTTP server
	BotToken            string   `json:"bot_token"`            // your Telegram bot token
	Translation         []string `json:"translation"`          // translation files
	Ads                 []string `json:"ads"`                  // ads files
	Images              string   `json:"images"`               // images directory
	MaintenanceResponse string   `json:"maintenance_response"` // the maintenance response
}

// StatusConfirmationSeconds represents a configureation of confirmation durations for each of specific statuses
type StatusConfirmationSeconds struct {
	Offline  int `json:"offline"`
	Online   int `json:"online"`
	NotFound int `json:"not_found"`
	Denied   int `json:"denied"`
}

// Config represents bot configuration
type Config struct {
	Debug                           bool                      `json:"debug"`                              // debug mode
	CheckGID                        bool                      `json:"check_gid"`                          // check goroutines ids
	ListenAddress                   string                    `json:"listen_address"`                     // the address to listen to
	Website                         string                    `json:"website"`                            // one of the following strings: "bongacams", "stripchat", "chaturbate", "livejasmin", "flirt4free", "streamate", "cam4"
	WebsiteLink                     string                    `json:"website_link"`                       // affiliate link to website
	PeriodSeconds                   int                       `json:"period_seconds"`                     // the period of querying models statuses
	CleaningPeriodSeconds           int                       `json:"cleaning_period_seconds"`            // the cleaning period
	MaxModels                       int                       `json:"max_models"`                         // maximum models per user
	TimeoutSeconds                  int                       `json:"timeout_seconds"`                    // HTTP timeout
	AdminID                         int64                     `json:"admin_id"`                           // admin Telegram ID
	AdminEndpoint                   string                    `json:"admin_endpoint"`                     // admin endpoint
	DBPath                          string                    `json:"db_path"`                            // path to the database
	BlockThreshold                  int                       `json:"block_threshold"`                    // do not send a message to the user after being blocked by him this number of times
	IntervalMs                      int                       `json:"interval_ms"`                        // queries interval per IP address for rate limited access
	SourceIPAddresses               []string                  `json:"source_ip_addresses"`                // source IP addresses for rate limited access
	DangerousErrorRate              string                    `json:"dangerous_error_rate"`               // dangerous error rate, warn admin if it is reached, format "1000/10000"
	EnableCookies                   bool                      `json:"enable_cookies"`                     // enable cookies, it can be useful to mitigate rate limits
	Headers                         [][2]string               `json:"headers"`                            // HTTP headers to make queries with
	StatPassword                    string                    `json:"stat_password"`                      // password for statistics
	ErrorReportingPeriodMinutes     int                       `json:"error_reporting_period_minutes"`     // the period of the error reports
	Endpoints                       map[string]endpoint       `json:"endpoints"`                          // the endpoints by simple name, used for the support of the bots in different languages accessing the same database
	HeavyUserRemainder              int                       `json:"heavy_user_remainder"`               // the maximum remainder of models to treat a user as heavy
	ReferralBonus                   int                       `json:"referral_bonus"`                     // number of additional subscriptions for a referrer
	FollowerBonus                   int                       `json:"follower_bonus"`                     // number of additional subscriptions for a new user registered by a referral link
	UsersOnlineEndpoint             []string                  `json:"users_online_endpoint"`              // the endpoint to fetch online users
	StatusConfirmationSeconds       StatusConfirmationSeconds `json:"status_confirmation_seconds"`        // a status is confirmed only if it lasts for at least this number of seconds
	OfflineNotifications            bool                      `json:"offline_notifications"`              // enable offline notifications
	SQLPrelude                      []string                  `json:"sql_prelude"`                        // run these SQL commands before any other
	EnableWeek                      bool                      `json:"enable_week"`                        // enable week command
	AffiliateLink                   string                    `json:"affiliate_link"`                     // affiliate link template
	SpecificConfig                  map[string]string         `json:"specific_config"`                    // the config for specific website
	TelegramTimeoutSeconds          int                       `json:"telegram_timeout_seconds"`           // the timeout for Telegram queries
	MaxSubscriptionsForPics         int                       `json:"max_subscriptions_for_pics"`         // the maximum amount of subscriptions for pics in a group chat
	KeepStatusesForDays             int                       `json:"keep_statuses_for_days"`             // keep statuses for this number of days
	MaxCleanSeconds                 int                       `json:"max_clean_seconds"`                  // maximum number of seconds to clean
	SubsConfirmationPeriodSeconds   int                       `json:"subs_confirmation_period_seconds"`   // subscriptions confirmation period
	NotificationsReadyPeriodSeconds int                       `json:"notifications_ready_period_seconds"` // notifications ready check period
	SpecialModels                   bool                      `json:"special_models"`                     // process special models
	ShowImages                      bool                      `json:"show_images"`                        // images support

	ErrorThreshold   int
	ErrorDenominator int
}

var fractionRegexp = regexp.MustCompile(`^(\d+)/(\d+)$`)

// ReadConfig read config
func ReadConfig(path string) *Config {
	file, err := os.Open(filepath.Clean(path))
	checkErr(err)
	defer func() { checkErr(file.Close()) }()
	return parseConfig(file)
}

func parseConfig(r io.Reader) *Config {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	cfg := &Config{ShowImages: true}
	err := decoder.Decode(cfg)
	checkErr(err)
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
