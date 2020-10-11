package lib

import (
	"math/rand"
	"time"
)

// CheckModelTest mimics checker
func CheckModelTest(client *Client, modelID string, headers [][2]string, dbg bool, _ map[string]string) StatusKind {
	return StatusOnline
}

// TestOnlineAPI returns random online models
func TestOnlineAPI(
	endpoint string,
	client *Client,
	headers [][2]string,
	dbg bool,
	_ map[string]string,
) (
	onlineModels map[string]OnlineModel,
	err error,
) {
	onlineModels = map[string]OnlineModel{}
	for i := 0; i < 300; i++ {
		modelID := randString(4)
		onlineModels[modelID] = OnlineModel{ModelID: modelID, Image: ""}
	}
	return
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func randString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}
