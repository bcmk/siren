package db

import "github.com/bcmk/siren/v2/lib/cmdlib"

// Notification represents a notification
type Notification struct {
	ID        int
	Endpoint  string
	ChatID    int64
	ChannelID string
	Status    cmdlib.StatusKind
	TimeDiff  *int
	ImageURL  string
	Viewers   *int
	ShowKind  cmdlib.ShowKind
	Social    bool
	Sound     bool
	Priority  int
	Kind      PacketKind
	Subject   string
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

// PerformanceLogKind represents a performance log entry kind
type PerformanceLogKind int

const (
	// PerformanceLogUpdateQuery represents a query for channel status updates
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
	MaxChannels          int
	Reports              int
	Blacklist            bool
	ShowImages           bool
	OfflineNotifications bool
	ShowSubject          bool
	CreatedAt            int64
	ChatType             *string
	MemberCount          *int
}

// Channel represents a channel
type Channel struct {
	ChannelID                string
	ConfirmedStatus          cmdlib.StatusKind
	UnconfirmedStatus        cmdlib.StatusKind
	UnconfirmedTimestamp     int
	PrevUnconfirmedStatus    cmdlib.StatusKind
	PrevUnconfirmedTimestamp int
}

// StatusChange represents a status change
type StatusChange struct {
	ChannelID string
	Status    cmdlib.StatusKind
	Timestamp int
}

// ConfirmedStatusChange represents a confirmed status change with previous status
type ConfirmedStatusChange struct {
	ChannelID  string
	Status     cmdlib.StatusKind
	PrevStatus cmdlib.StatusKind
	Timestamp  int
}

// Subscription represents a subscription
type Subscription struct {
	ChatID    int64
	ChannelID string
	Endpoint  string
}
