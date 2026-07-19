package db

import "github.com/bcmk/siren/v3/lib/cmdlib"

// Notification represents a notification.
type Notification struct {
	ID       int
	Endpoint string
	UserID   UserID

	// Nil if the streamer is unknown (e.g. denied subscription replies)
	StreamerID *int

	// Only populated when joined with streamers
	Nickname string

	Status   cmdlib.StatusKind
	TimeDiff *int
	ImageURL string
	Viewers  *int
	ShowKind cmdlib.ShowKind
	Social   bool
	Sound    bool
	Priority Priority
	Kind     PacketKind
	// Command is the command this notification answers, empty if none.
	// A deferred reply (an add result, an online-list picture)
	// carries it across the queue,
	// so its send logs the command that asked for it.
	Command        string
	Subject        string
	SilentMessages bool
}

// UserID is a user's stable surrogate id (users.id), distinct from the mutable
// chat_id. Typed so the compiler keeps the two from being swapped.
type UserID int64

// Priority represents a message priority
type Priority int

const (
	// PriorityHigh is for user-initiated replies and admin commands
	PriorityHigh Priority = 0

	// PriorityLow is for bulk notifications and background messages
	PriorityLow Priority = 1
)

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

	// MaintenancePacket represents a maintenance-window message.
	// Its send results skip all database bookkeeping,
	// so it can be sent while migrations run.
	MaintenancePacket PacketKind = 4
)

// PerformanceLogKind represents a performance log entry kind
type PerformanceLogKind int

const (
	// PerformanceLogUpdateQuery represents a query for streamer status updates
	PerformanceLogUpdateQuery PerformanceLogKind = 0

	// PerformanceLogExistenceQuery represents an existence check query
	PerformanceLogExistenceQuery PerformanceLogKind = 3

	// PerformanceLogUpdateProcessing represents status update processing
	PerformanceLogUpdateProcessing PerformanceLogKind = 1

	// PerformanceLogImageDownload represents an image download
	PerformanceLogImageDownload PerformanceLogKind = 2
)

// User represents a chat
type User struct {
	ChatID               int64
	UserID               UserID
	MaxSubs              int
	Reports              int
	Blacklist            bool
	ShowImages           bool
	OfflineNotifications bool
	ShowSubject          bool
	SilentMessages       bool
	CreatedAt            int64
	ChatType             *string
	MemberCount          *int
}

// Streamer represents a streamer
type Streamer struct {
	ID                       int
	Nickname                 string
	ConfirmedStatus          cmdlib.StatusKind
	UnconfirmedStatus        cmdlib.StatusKind
	UnconfirmedTimestamp     int
	PrevUnconfirmedStatus    cmdlib.StatusKind
	PrevUnconfirmedTimestamp int
}

// StatusChange represents a status change
type StatusChange struct {
	Nickname  string
	Status    cmdlib.StatusKind
	Timestamp int
}

// ConfirmedStatusChange represents a confirmed status change with previous status
type ConfirmedStatusChange struct {
	StreamerID int
	Nickname   string
	Status     cmdlib.StatusKind
	PrevStatus cmdlib.StatusKind
	Timestamp  int
}

// PendingSubscription represents an unconfirmed subscription
type PendingSubscription struct {
	UserID   UserID
	Nickname string
	Endpoint string
	Referral bool
	// Command is the command that asked for the subscription.
	// The confirmation reply lands much later,
	// so it is kept here to tag that reply with what it answers.
	Command string
}
