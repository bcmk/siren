package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type config struct {
	BotPath        string `json:"bot_path"`
	BotToken       string `json:"bot_token"`
	PeriodSeconds  int    `json:"period_seconds"`
	MaxModels      int    `json:"max_models"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	AdminID        int64  `json:"admin_id"`
	LanguageCode   string `json:"language_code"`
	DBPath         string `json:"db_path"`
}

func readConfig(path string) *config {
	file, err := os.Open(filepath.Clean(path))
	checkErr(err)
	defer func() { checkErr(file.Close()) }()
	decoder := json.NewDecoder(file)
	cfg := &config{}
	err = decoder.Decode(cfg)
	checkErr(err)
	if false ||
		cfg.BotPath == "" ||
		cfg.BotToken == "" ||
		cfg.PeriodSeconds == 0 ||
		cfg.MaxModels == 0 ||
		cfg.TimeoutSeconds == 0 ||
		cfg.AdminID == 0 ||
		cfg.DBPath == "" {

		panic("config error")
	}
	return cfg
}
