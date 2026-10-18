package main

import (
	"bytes"
	"context"
	"maps"
	"time"

	texttemplate "text/template"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/bcmk/siren/v6/lib/cmdlib"
)

// renderParams is what a message needs to produce its text,
// held until dispatch because the mention a command carries depends on the chat.
type renderParams struct {
	templates *texttemplate.Template
	key       string
	data      tplData
}

// render produces the text, with {{ command }} writing the mention for this chat.
// The clone keeps that binding local,
// sharing the parse trees and allocating one small struct per associated template plus two maps.
// That costs microseconds, and endpointCooldown paces sends at 60ms however deep the queue.
func (d *renderParams) render(mention string) string {
	tpl := texttemplate.Must(d.templates.Clone()).Funcs(cmdlib.CommandFuncs(mention))
	return templateToString(tpl, d.key, d.data)
}

// image is what a photo shows: one from a site, downloaded once queued, or one the bot ships.
type image struct {
	// key names the image in a bot's imageCache: its URL, or a shipped image's file name.
	key string
	// data holds a shipped image's bytes, nil for an image from a site.
	data []byte
}

// shippedImage is the image a translation ships with the bot, nil for none.
func shippedImage(tr *cmdlib.Translation) *image {
	if len(tr.ImageBytes) == 0 {
		return nil
	}
	return &image{key: tr.Image, data: tr.ImageBytes}
}

// asDeferredSendable packages the render as the message its translation calls for:
// a photo when it carries one, text otherwise.
func (d *renderParams) asDeferredSendable(
	tr *cmdlib.Translation,
	notify bool,
	img *image,
	markup models.ReplyMarkup,
) sendable {
	if img == nil {
		msg := d.asDeferredText(notify, tr.DisablePreview, tr.Parse)
		msg.ReplyMarkup = markup
		return msg
	}
	photo := d.asDeferredPhoto(notify, tr.DisablePreview, tr.Parse, img)
	photo.ReplyMarkup = markup
	return photo
}

// asDeferredText packages the render as a text message;
// dispatch fills the text in once the chat and its mention are known.
func (d *renderParams) asDeferredText(notify, disablePreview bool, parse cmdlib.ParseKind) *messageParams {
	m := textMessage("", notify, disablePreview, parse)
	m.renderParams = d
	return m
}

// asDeferredPhoto packages the render as a photo;
// dispatch fills the caption in, as asDeferredText does the text.
func (d *renderParams) asDeferredPhoto(
	notify, disablePreview bool,
	parse cmdlib.ParseKind,
	img *image,
) *photoParams {
	return &photoParams{
		SendPhotoParams: &bot.SendPhotoParams{
			DisableNotification: !notify,
			ParseMode:           parseMode(parse),
		},
		image:          img,
		disablePreview: disablePreview,
		renderParams:   d,
		textRender:     d,
	}
}

type sendable interface {
	chatID() int64
	// setChatID gives a message its chat, which it is built without:
	// trySend resolves it from userID at dispatch,
	// and sendMaintenance is the one caller that sets it itself.
	setChatID(int64)
	// render produces the text once the chat is known,
	// so a command it names can be addressed to the bot.
	// It renders once: a retry resends that text, and the queue stops holding the data.
	// A photo keeps its data, for the text it becomes without its image.
	// A retry needs no new mention: a group becoming a supergroup keeps the chat id negative.
	render(mention string)
	send(ctx context.Context, b *bot.Bot) (*models.Message, error)
}

type messageParams struct {
	*bot.SendMessageParams
	// renderParams is nil when the text is already final.
	renderParams *renderParams
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

func (m *messageParams) render(mention string) {
	if m.renderParams == nil {
		return
	}
	m.Text = m.renderParams.render(mention)
	m.renderParams = nil
}

func (m *messageParams) send(ctx context.Context, b *bot.Bot) (*models.Message, error) {
	// See messageParams.chatID: a send before render is a bug.
	if m.renderParams != nil {
		panic("send before render")
	}
	return b.SendMessage(ctx, m.SendMessageParams)
}

type photoParams struct {
	*bot.SendPhotoParams
	image *image
	// sentImage is the image a dispatch sends the photo with:
	// its own, or the image-unavailable image when its download failed.
	sentImage *image
	// fileID names the sent image on Telegram; dispatch sets it when the bot holds one.
	fileID string
	// upload holds the bytes a send without an id uploads,
	// downloaded at downloadedAt for an image from a site.
	upload       []byte
	downloadedAt time.Time
	// downloadFailed has dispatch show the placeholder.
	downloadFailed bool
	// disablePreview is unused by a photo, and kept for the text it may become.
	disablePreview bool
	// renderParams is nil when the caption is already final.
	renderParams *renderParams
	// textRender outlives the caption's render, for the text the photo becomes without its image.
	textRender *renderParams
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

func (p *photoParams) render(mention string) {
	if p.renderParams == nil {
		return
	}
	p.Caption = p.renderParams.render(mention)
	p.renderParams = nil
}

func (p *photoParams) send(ctx context.Context, b *bot.Bot) (*models.Message, error) {
	// See messageParams.chatID: a send before render is a bug.
	if p.renderParams != nil {
		panic("send before render")
	}
	if p.fileID != "" {
		p.Photo = &models.InputFileString{Data: p.fileID}
		return b.SendPhoto(ctx, p.SendPhotoParams)
	}
	// Create reader here rather than pass it in.
	// Otherwise retries consume it and we must rewind it.
	p.Photo = &models.InputFileUpload{Filename: "preview", Data: bytes.NewReader(p.upload)}
	return b.SendPhoto(ctx, p.SendPhotoParams)
}

// toText swaps a photo for text with the same caption, dropping any render still pending.
func (p *photoParams) toText() *messageParams {
	params := &bot.SendMessageParams{
		ChatID:              p.ChatID,
		Text:                p.Caption,
		ParseMode:           p.ParseMode,
		DisableNotification: p.DisableNotification,
		ReplyMarkup:         p.ReplyMarkup,
	}
	if p.disablePreview {
		params.LinkPreviewOptions = &models.LinkPreviewOptions{IsDisabled: bot.True()}
	}
	return &messageParams{SendMessageParams: params}
}

// toUnavailableText is the photo as text rendered afresh with image_unavailable set,
// which the online template reads to say the image is unavailable.
// The flag goes into a copy, so the photo keeps its data for a later dispatch.
func (p *photoParams) toUnavailableText() *messageParams {
	d := *p.textRender
	d.data = tplData{}
	maps.Copy(d.data, p.textRender.data)
	d.data["image_unavailable"] = true
	m := p.toText()
	m.renderParams = &d
	return m
}
