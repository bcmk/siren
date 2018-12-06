package main

import (
	"encoding/json"
	"os"
)

type config struct {
	BotToken      string `json:"bot_token"`
	PeriodSeconds int    `json:"period_seconds"`
	MaxModels     int    `json:"max_models"`
}

func readConfig() *config {
	file, err := os.Open("conf.json")
	checkErr(err)
	defer file.Close()
	decoder := json.NewDecoder(file)
	cfg := &config{}
	err = decoder.Decode(cfg)
	checkErr(err)
	if cfg.BotToken == "" || cfg.PeriodSeconds == 0 || cfg.MaxModels == 0 {
		panic("config error")
	}
	return cfg
}
