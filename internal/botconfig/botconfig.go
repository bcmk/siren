// Package botconfig represents bot configuration
package botconfig

import (
	"errors"
	"log"
	"strings"
	"time"

	"github.com/bcmk/siren/v2/lib/cmdlib"
	"github.com/spf13/viper"
)

var checkErr = cmdlib.CheckErr

type endpoint struct {
	ListenPath          cmdlib.Secret `mapstructure:"listen_path"`          // the path excluding domain to listen to, the good choice is "/your-telegram-bot-token"
	WebhookDomain       string        `mapstructure:"webhook_domain"`       // the domain listening to the webhook
	BotToken            cmdlib.Secret `mapstructure:"bot_token"`            // your Telegram bot token
	Translation         []string      `mapstructure:"translation"`          // translation files
	Ads                 []string      `mapstructure:"ads"`                  // ads files
	Images              string        `mapstructure:"images"`               // images directory
	MaintenanceResponse string        `mapstructure:"maintenance_response"` // the maintenance response
}

// StatusConfirmationSeconds represents configuration of confirmation durations
type StatusConfirmationSeconds struct {
	Offline int `mapstructure:"offline"`
	Online  int `mapstructure:"online"`
}

// Config represents bot configuration
type Config struct {
	Debug                           bool                      `mapstructure:"debug"`                              // debug mode
	CheckGID                        bool                      `mapstructure:"check_gid"`                          // check goroutines ids
	ListenAddress                   string                    `mapstructure:"listen_address"`                     // the address to listen to
	Website                         string                    `mapstructure:"website"`                            // one of the following strings: "bongacams", "stripchat", "chaturbate", "livejasmin", "flirt4free", "streamate", "cam4", "twitch", "kick", "myfreecams"
	WebsiteLink                     string                    `mapstructure:"website_link"`                       // affiliate link to website
	PeriodSeconds                   int                       `mapstructure:"period_seconds"`                     // the period of querying streamer statuses
	MaintainDBPeriodSeconds         int                       `mapstructure:"maintain_db_period_seconds"`         // the maintain DB period
	MaxSubs                         int                       `mapstructure:"max_subs"`                           // maximum subscriptions per user
	AdminID                         int64                     `mapstructure:"admin_id"`                           // admin Telegram ID
	AdminEndpoint                   string                    `mapstructure:"admin_endpoint"`                     // admin endpoint
	DBConnectionString              cmdlib.Secret             `mapstructure:"db_connection_string"`               // database connection string
	Endpoints                       map[string]endpoint       `mapstructure:"endpoints"`                          // the endpoints by simple name, used for the support of the bots in different languages accessing the same database
	HeavyUserRemainder              int                       `mapstructure:"heavy_user_remainder"`               // the maximum remainder of subscriptions to treat a user as heavy
	ReferralBonus                   int                       `mapstructure:"referral_bonus"`                     // number of additional subscriptions for a referrer
	FollowerBonus                   int                       `mapstructure:"follower_bonus"`                     // number of additional subscriptions for a new user registered by a referral link
	StatusConfirmationSeconds       StatusConfirmationSeconds `mapstructure:"status_confirmation_seconds"`        // a status is confirmed only if it lasts for at least this number of seconds
	OfflineNotifications            bool                      `mapstructure:"offline_notifications"`              // enable offline notifications
	SQLPrelude                      []string                  `mapstructure:"sql_prelude"`                        // run these SQL commands before any other
	EnableWeek                      bool                      `mapstructure:"enable_week"`                        // enable week command
	AffiliateLink                   string                    `mapstructure:"affiliate_link"`                     // affiliate link template
	TelegramTimeoutSeconds          int                       `mapstructure:"telegram_timeout_seconds"`           // the timeout for Telegram queries
	MaxSubscriptionsForPics         int                       `mapstructure:"max_subscriptions_for_pics"`         // the maximum amount of subscriptions for pics in a group chat
	SubsConfirmationPeriodSeconds   int                       `mapstructure:"subs_confirmation_period_seconds"`   // subscriptions confirmation period
	NotificationsReadyPeriodSeconds int                       `mapstructure:"notifications_ready_period_seconds"` // notifications ready check period
	ShowImages                      bool                      `mapstructure:"show_images"`                        // images support
	AdChancePercent                 int                       `mapstructure:"ad_chance_percent"`                  // probability of showing an ad (0–100)
	WhitelistChats                  []int64                   `mapstructure:"whitelist_chats"`                    // if set, only these chats are processed
}

// ReadConfig reads the bot config from cfgPath. cfgPath must be non-empty.
func ReadConfig(cfgPath string) *Config {
	v := viper.New()
	v.SetConfigType("json")
	v.SetConfigFile(cfgPath)
	if err := v.ReadInConfig(); err != nil {
		log.Fatalf("error reading %q: %v", cfgPath, err)
	}
	log.Printf("successfully read config %q", cfgPath)

	v.SetEnvPrefix("XRN")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	cfg := &Config{ShowImages: true, AdChancePercent: 20}
	cmdlib.BindEnvForConfig(v, cfg)
	checkErr(v.Unmarshal(&cfg, cmdlib.StrictConfigDecoder))
	checkErr(checkConfig(cfg))

	return cfg
}

func checkConfig(cfg *Config) error {
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
	if cfg.MaxSubs == 0 {
		return errors.New("configure max_subs")
	}
	if cfg.AdminID == 0 {
		return errors.New("configure admin_id")
	}
	if cfg.DBConnectionString == "" {
		return errors.New("configure db_connection_string")
	}
	if cfg.Website == "" {
		return errors.New("configure website")
	}
	if cfg.WebsiteLink == "" {
		return errors.New("configure website_link")
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
	if cfg.SubsConfirmationPeriodSeconds == 0 {
		return errors.New("configure subs_confirmation_period_seconds")
	}
	if cfg.NotificationsReadyPeriodSeconds == 0 {
		return errors.New("configure notifications_ready_period_seconds")
	}

	return nil
}

// TelegramTimeout returns the configured Telegram HTTP timeout.
func (c *Config) TelegramTimeout() time.Duration {
	return time.Duration(c.TelegramTimeoutSeconds) * time.Second
}

// ChatWhitelisted returns true if the chat is whitelisted or if no whitelist
// is configured.
func (c *Config) ChatWhitelisted(chatID int64) bool {
	if len(c.WhitelistChats) == 0 {
		return true
	}
	for _, id := range c.WhitelistChats {
		if id == chatID {
			return true
		}
	}
	return false
}
