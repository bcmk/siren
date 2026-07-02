package main

import (
	"testing"

	"github.com/bcmk/siren/v3/internal/botconfig"
	"github.com/go-telegram/bot/models"
)

// A non-whitelisted chat's command during the startup window must not register
// a waitingUser, or finishStartup's EnsureUser would materialize a row for it.
func TestMaintenanceReplyWhitelistGate(t *testing.T) {
	w := &worker{
		cfg:      &botconfig.Config{WhitelistChats: []int64{1}}, // 999 is not listed
		botNames: map[string]string{"ep": "bot"},
	}
	waitingUsers := map[waitingUser]bool{}
	w.maintenanceReply(incomingPacket{
		endpoint: "ep",
		message:  &models.Update{Message: &models.Message{Text: "start", Chat: models.Chat{ID: 999}}},
	}, waitingUsers)
	if len(waitingUsers) != 0 {
		t.Errorf("non-whitelisted chat registered %d waitingUsers, want 0", len(waitingUsers))
	}
}
