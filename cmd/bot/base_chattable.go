package main

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type sendable interface {
	chatID() int64
	send(ctx context.Context, b *bot.Bot) (*models.Message, error)
}

type messageParams struct {
	*bot.SendMessageParams
}

func (m *messageParams) chatID() int64 {
	return m.ChatID.(int64)
}

func (m *messageParams) send(ctx context.Context, b *bot.Bot) (*models.Message, error) {
	return b.SendMessage(ctx, m.SendMessageParams)
}

type photoParams struct {
	*bot.SendPhotoParams
}

func (p *photoParams) chatID() int64 {
	return p.ChatID.(int64)
}

func (p *photoParams) send(ctx context.Context, b *bot.Bot) (*models.Message, error) {
	return b.SendPhoto(ctx, p.SendPhotoParams)
}
