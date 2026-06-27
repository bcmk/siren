package main

import (
	"bytes"
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type sendable interface {
	chatID() int64
	setChatID(int64)
	send(ctx context.Context, b *bot.Bot) (*models.Message, error)
}

type messageParams struct {
	*bot.SendMessageParams
}

func (m *messageParams) chatID() int64 {
	// setChatID must run first (trySend or sendMaintenance).
	// A read before it is a bug in a new send path,
	// so fail here rather than silently POSTing to chat 0.
	id, ok := m.ChatID.(int64)
	if !ok {
		panic("chatID read before setChatID")
	}
	return id
}

func (m *messageParams) setChatID(id int64) {
	m.ChatID = id
}

func (m *messageParams) send(ctx context.Context, b *bot.Bot) (*models.Message, error) {
	return b.SendMessage(ctx, m.SendMessageParams)
}

type photoParams struct {
	*bot.SendPhotoParams
	imageData []byte
}

func (p *photoParams) chatID() int64 {
	// See messageParams.chatID: a read before setChatID is a bug.
	id, ok := p.ChatID.(int64)
	if !ok {
		panic("chatID read before setChatID")
	}
	return id
}

func (p *photoParams) setChatID(id int64) {
	p.ChatID = id
}

func (p *photoParams) send(ctx context.Context, b *bot.Bot) (*models.Message, error) {
	// Create reader here rather than pass it in.
	// Otherwise retries consume it and we must rewind it.
	p.Photo = &models.InputFileUpload{Filename: "preview", Data: bytes.NewReader(p.imageData)}
	return b.SendPhoto(ctx, p.SendPhotoParams)
}

func (p *photoParams) toText() *messageParams {
	return &messageParams{&bot.SendMessageParams{
		ChatID:              p.ChatID,
		Text:                p.Caption,
		ParseMode:           p.ParseMode,
		DisableNotification: p.DisableNotification,
	}}
}
