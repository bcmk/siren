package main

import tg "github.com/bcmk/telegram-bot-api"

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

type documentConfig struct{ tg.DocumentConfig }

func (m *documentConfig) baseChat() *tg.BaseChat {
	return &m.BaseChat
}
