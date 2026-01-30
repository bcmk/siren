package main

import tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"

type baseChattable interface {
	tg.Chattable
	baseChat() *tg.BaseChat
}

type messageConfig struct{ tg.MessageConfig }

func (m *messageConfig) baseChat() *tg.BaseChat {
	return &m.BaseChat
}

type photoConfig struct{ tg.PhotoConfig }

func (m *photoConfig) baseChat() *tg.BaseChat {
	return &m.BaseChat
}
