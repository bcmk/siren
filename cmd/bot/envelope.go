package main

import (
	"bytes"

	"github.com/bradfitz/go-smtpd/smtpd"
	"github.com/jhillyerd/enmime"
)

type env struct {
	*smtpd.BasicEnvelope
	from  smtpd.MailAddress
	data  []byte
	mime  *enmime.Envelope
	rcpts []smtpd.MailAddress
	ch    chan<- *env
}

// Close implements smtpd.Envelope.Close
func (e *env) Close() error {
	mime, err := enmime.ReadEnvelope(bytes.NewReader(e.data))
	if err != nil {
		return err
	}
	e.mime = mime
	e.ch <- e
	return nil
}

// Write implements smtpd.Envelope.Write
func (e *env) Write(line []byte) error {
	e.data = append(e.data, line...)
	return nil
}

// AddRecipient implements smtpd.Envelope.AddRecipient
func (e *env) AddRecipient(rcpt smtpd.MailAddress) error {
	e.rcpts = append(e.rcpts, rcpt)
	return e.BasicEnvelope.AddRecipient(rcpt)
}
