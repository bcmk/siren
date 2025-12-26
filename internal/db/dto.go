package db

import "github.com/bcmk/siren/lib/cmdlib"

// Notification represents a notification
type Notification struct {
	ID       int
	Endpoint string
	ChatID   int64
	ModelID  string
	Status   cmdlib.StatusKind
	TimeDiff *int
	ImageURL string
	Social   bool
	Sound    bool
	Priority int
	Kind     PacketKind
}

// PacketKind represents a notification kind
type PacketKind int

const (
	// NotificationPacket represents a notification packet
	NotificationPacket PacketKind = 0

	// ReplyPacket represents a reply packet
	ReplyPacket PacketKind = 1

	// AdPacket represents an advertisement packet
	AdPacket PacketKind = 2

	// MessagePacket represents a message packet
	MessagePacket PacketKind = 3
)

// User represents a chat
type User struct {
	ChatID               int64
	MaxModels            int
	Reports              int
	Blacklist            bool
	ShowImages           bool
	OfflineNotifications bool
}

// Model represents a model
type Model struct {
	ModelID                  string
	ConfirmedStatus          cmdlib.StatusKind
	UnconfirmedStatus        cmdlib.StatusKind
	UnconfirmedTimestamp     int
	PrevUnconfirmedStatus    cmdlib.StatusKind
	PrevUnconfirmedTimestamp int
}

// StatusChange represents a status change
type StatusChange struct {
	ModelID   string
	Status    cmdlib.StatusKind
	Timestamp int
}

// Subscription represents a supscription
type Subscription struct {
	ChatID   int64
	ModelID  string
	Endpoint string
}
