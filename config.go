package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type config struct {
	Website           string `json:"website"`
	ListenPath        string `json:"listen_path"`
	ListenAddress     string `json:"listen_address"`
	BotToken          string `json:"bot_token"`
	PeriodSeconds     int    `json:"period_seconds"`
	MaxModels         int    `json:"max_models"`
	TimeoutSeconds    int    `json:"timeout_seconds"`
	AdminID           int64  `json:"admin_id"`
	DBPath            string `json:"db_path"`
	Certificate       string `json:"certificate"` // omit if under a proxy
	Key               string `json:"key"`         // omit if under a proxy
	NotFoundThreshold int    `json:"not_found_threshold"`
	Translations      string `json:"translations"`
	Debug             bool   `json:"debug"`
}

func readConfig(path string) *config {
	file, err := os.Open(filepath.Clean(path))
	checkErr(err)
	defer func() { checkErr(file.Close()) }()
	decoder := json.NewDecoder(file)
	cfg := &config{}
	err = decoder.Decode(cfg)
	checkErr(err)
	if !checkConfig(cfg) {
		panic("config error")
	}
	return cfg
}

func checkConfig(cfg *config) bool {
	return true &&
		cfg.ListenPath != "" &&
		cfg.BotToken != "" &&
		cfg.PeriodSeconds != 0 &&
		cfg.MaxModels != 0 &&
		cfg.TimeoutSeconds != 0 &&
		cfg.AdminID != 0 &&
		cfg.DBPath != "" &&
		cfg.ListenAddress != "" &&
		cfg.NotFoundThreshold != 0 &&
		cfg.Website != "" &&
		cfg.Translations != ""
}
