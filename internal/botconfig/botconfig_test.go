package botconfig

import "testing"

func TestValidateSubsTiers(t *testing.T) {
	tests := []struct {
		name    string
		in      []SubsTier
		wantErr bool
	}{
		{
			name: "empty is valid (disabled)",
			in:   nil,
		},
		{
			name: "valid custom tiers",
			in:   []SubsTier{{Count: 10, Cost: 500}, {Count: 100, Cost: 3000}},
		},
		{
			name: "single tier",
			in:   []SubsTier{{Count: 10, Cost: 500}},
		},
		{
			name: "equal price is allowed",
			in:   []SubsTier{{Count: 10, Cost: 500}, {Count: 20, Cost: 1000}},
		},
		{
			name:    "zero count",
			in:      []SubsTier{{Count: 0, Cost: 500}},
			wantErr: true,
		},
		{
			name:    "negative count",
			in:      []SubsTier{{Count: -1, Cost: 500}},
			wantErr: true,
		},
		{
			name:    "zero price",
			in:      []SubsTier{{Count: 10, Cost: 0}},
			wantErr: true,
		},
		{
			name:    "equal count not ascending",
			in:      []SubsTier{{Count: 10, Cost: 500}, {Count: 10, Cost: 400}},
			wantErr: true,
		},
		{
			name:    "descending count",
			in:      []SubsTier{{Count: 20, Cost: 1000}, {Count: 10, Cost: 400}},
			wantErr: true,
		},
		{
			name:    "increasing price",
			in:      []SubsTier{{Count: 10, Cost: 300}, {Count: 20, Cost: 800}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSubsTiers(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

// validEndpoint is an endpoint with every field checkConfig demands.
func validEndpoint() endpoint {
	return endpoint{
		ListenPath:          "/x",
		WebhookDomain:       "bot.example.invalid",
		BotToken:            "123:token",
		Translation:         []string{"t.yaml"},
		Images:              "img",
		MaintenanceResponse: "later",
	}
}

// validConfig is a config checkConfig accepts, so a test can mangle one field and see it refused.
func validConfig(e endpoint) *Config {
	return &Config{
		Endpoints:                       map[string]endpoint{"test": e},
		ListenAddress:                   ":0",
		OwnerEndpoint:                   "test",
		PeriodSeconds:                   1,
		MaxSubs:                         1,
		OwnerID:                         1,
		DBConnectionString:              "postgres://",
		Website:                         "siren",
		WebsiteLink:                     "https://example.invalid",
		HeavyUserRemainder:              1,
		ReferralBonus:                   1,
		FollowerBonus:                   1,
		TelegramTimeoutSeconds:          10,
		MaxSubscriptionsForPics:         10,
		SubsConfirmationPeriodSeconds:   1,
		NotificationsReadyPeriodSeconds: 1,
	}
}

// TestCheckConfigRequiresEndpointFields pins what an endpoint cannot start without.
// Updates arrive by webhook alone and the web app URLs are built on the domain,
// so an endpoint missing it is deaf and links nowhere:
// worth refusing at startup rather than running and never answering.
func TestCheckConfigRequiresEndpointFields(t *testing.T) {
	tests := []struct {
		name    string
		mangle  func(*endpoint)
		wantErr bool
	}{
		{name: "complete", mangle: func(*endpoint) {}},
		{name: "no listen_path", mangle: func(e *endpoint) { e.ListenPath = "" }, wantErr: true},
		{name: "no webhook_domain", mangle: func(e *endpoint) { e.WebhookDomain = "" }, wantErr: true},
		{name: "no bot_token", mangle: func(e *endpoint) { e.BotToken = "" }, wantErr: true},
		{name: "no translation", mangle: func(e *endpoint) { e.Translation = nil }, wantErr: true},
		{name: "no images", mangle: func(e *endpoint) { e.Images = "" }, wantErr: true},
		{
			name:    "no maintenance_response",
			mangle:  func(e *endpoint) { e.MaintenanceResponse = "" },
			wantErr: true,
		},
		// Both readers want a bare host: one prefixes a scheme, the other joins a path onto it.
		{
			name:    "webhook_domain carrying a scheme",
			mangle:  func(e *endpoint) { e.WebhookDomain = "https://bot.example.invalid" },
			wantErr: true,
		},
		{
			name:    "webhook_domain with a trailing slash",
			mangle:  func(e *endpoint) { e.WebhookDomain = "bot.example.invalid/" },
			wantErr: true,
		},
		// A scheme mistyped with one slash, and a bare path: neither holds a host either reader can use.
		{
			name:    "webhook_domain with a half-typed scheme",
			mangle:  func(e *endpoint) { e.WebhookDomain = "https:/bot.example.invalid" },
			wantErr: true,
		},
		{
			name:    "webhook_domain written as a path",
			mangle:  func(e *endpoint) { e.WebhookDomain = "/bot.example.invalid" },
			wantErr: true,
		},
		// Shapes holding no slash at all, which a rule written over slashes would wave through.
		{
			name:    "webhook_domain with a scheme and no slashes",
			mangle:  func(e *endpoint) { e.WebhookDomain = "https:bot.example.invalid" },
			wantErr: true,
		},
		{
			name:    "webhook_domain carrying a query",
			mangle:  func(e *endpoint) { e.WebhookDomain = "bot.example.invalid?x" },
			wantErr: true,
		},
		// A port is part of a host, so it stays allowed.
		{name: "webhook_domain with a port", mangle: func(e *endpoint) { e.WebhookDomain = "bot.example.invalid:8443" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := validEndpoint()
			tt.mangle(&e)
			if err := checkConfig(validConfig(e)); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}
