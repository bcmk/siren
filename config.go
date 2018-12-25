package main

import (
	"encoding/json"
	"os"
)

type config struct {
	BotToken       string `json:"bot_token"`
	PeriodSeconds  int    `json:"period_seconds"`
	MaxModels      int    `json:"max_models"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func readConfig() *config {
	file, err := os.Open("conf.json")
	checkErr(err)
	defer checkErr(file.Close())
	decoder := json.NewDecoder(file)
	cfg := &config{}
	err = decoder.Decode(cfg)
	checkErr(err)
	if cfg.BotToken == "" || cfg.PeriodSeconds == 0 || cfg.MaxModels == 0 || cfg.TimeoutSeconds == 0 {
		panic("config error")
	}
	return cfg
}
