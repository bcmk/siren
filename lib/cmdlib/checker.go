package cmdlib

import (
	"encoding/json"
	"fmt"
	"time"
)

// StatusRequest is the interface for status requests
type StatusRequest interface {
	isStatusRequest()
}

// CheckerResults is the interface for status results
type CheckerResults interface {
	isCheckerResults()
	Duration() time.Duration
	Failed() bool
	Count() int
	ExtraLogFields() map[string]any
}

// OnlineListRequest requests statuses for all online streamers.
// Names in Poll fall back to QueryStatus when not in the bulk result.
type OnlineListRequest struct {
	Poll      []string
	ResultsCh chan<- CheckerResults
}

func (r *OnlineListRequest) isStatusRequest() {}

// OnlineListResults contains results for OnlineListRequest
type OnlineListResults struct {
	Streamers  map[string]StreamerInfo
	PollCount  int
	PollErrors []string
	duration   time.Duration
	failed     bool
}

func (r *OnlineListResults) isCheckerResults() {}

// MarshalJSON serialises the result so consumers (e.g. adapter-mfc)
// can return it over HTTP without exposing the unexported fields.
func (r *OnlineListResults) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Streamers  map[string]StreamerInfo `json:"streamers"`
		PollCount  int                     `json:"poll_count,omitempty"`
		PollErrors []string                `json:"poll_errors,omitempty"`
		Duration   time.Duration           `json:"duration"`
		Failed     bool                    `json:"failed"`
	}{r.Streamers, r.PollCount, r.PollErrors, r.duration, r.failed})
}

// Duration returns the elapsed time for the request.
func (r *OnlineListResults) Duration() time.Duration { return r.duration }

// Failed returns whether the request failed.
func (r *OnlineListResults) Failed() bool { return r.failed }

// Count returns the number of streamers in the result.
func (r *OnlineListResults) Count() int { return len(r.Streamers) }

// ExtraLogFields returns extra fields for performance logging.
func (r *OnlineListResults) ExtraLogFields() map[string]any {
	return map[string]any{
		"poll_count":  r.PollCount,
		"poll_errors": len(r.PollErrors),
	}
}

// NewOnlineListResults creates a successful OnlineListResults.
func NewOnlineListResults(streamers map[string]StreamerInfo, duration time.Duration) *OnlineListResults {
	return &OnlineListResults{Streamers: streamers, duration: duration}
}

// NewOnlineListResultsFailed creates a failed OnlineListResults.
func NewOnlineListResultsFailed() *OnlineListResults {
	return &OnlineListResults{failed: true}
}

// FixedListOnlineRequest requests statuses for specific streamers
type FixedListOnlineRequest struct {
	Streamers map[string]bool
	ResultsCh chan<- CheckerResults
}

func (r *FixedListOnlineRequest) isStatusRequest() {}

// FixedListOnlineResults contains results for FixedListOnlineRequest
type FixedListOnlineResults struct {
	RequestedStreamers map[string]bool
	Streamers          map[string]StreamerInfo
	duration           time.Duration
	failed             bool
}

func (r *FixedListOnlineResults) isCheckerResults() {}

// Duration returns the elapsed time for the request.
func (r *FixedListOnlineResults) Duration() time.Duration { return r.duration }

// Failed returns whether the request failed.
func (r *FixedListOnlineResults) Failed() bool { return r.failed }

// Count returns the number of streamers in the result.
func (r *FixedListOnlineResults) Count() int { return len(r.Streamers) }

// ExtraLogFields returns extra fields for performance logging.
func (r *FixedListOnlineResults) ExtraLogFields() map[string]any { return nil }

// NewFixedListOnlineResults creates a successful FixedListOnlineResults.
func NewFixedListOnlineResults(
	requestedStreamers map[string]bool,
	streamers map[string]StreamerInfo,
	duration time.Duration,
) *FixedListOnlineResults {
	return &FixedListOnlineResults{
		RequestedStreamers: requestedStreamers,
		Streamers:          streamers,
		duration:           duration,
	}
}

// NewFixedListOnlineResultsFailed creates a failed FixedListOnlineResults.
func NewFixedListOnlineResultsFailed() *FixedListOnlineResults {
	return &FixedListOnlineResults{failed: true}
}

// FixedListStatusRequest checks if specific streamers exist
type FixedListStatusRequest struct {
	Streamers map[string]bool
	ResultsCh chan<- *ExistenceListResults
}

func (r *FixedListStatusRequest) isStatusRequest() {}

// SingleStatusRequest is FixedListStatusRequest dispatched via QueryStatus
// for one streamer; used when the checker has no batched surface.
type SingleStatusRequest struct {
	Streamer  string
	ResultsCh chan<- *ExistenceListResults
}

func (r *SingleStatusRequest) isStatusRequest() {}

// ExistenceListResults contains results for ExistenceListRequest
type ExistenceListResults struct {
	Streamers map[string]StreamerInfoWithStatus
	duration  time.Duration
	failed    bool
}

func (r *ExistenceListResults) isCheckerResults() {}

// Duration returns the elapsed time for the request.
func (r *ExistenceListResults) Duration() time.Duration { return r.duration }

// Failed returns whether the request failed.
func (r *ExistenceListResults) Failed() bool { return r.failed }

// Count returns the number of streamers in the result.
func (r *ExistenceListResults) Count() int { return len(r.Streamers) }

// ExtraLogFields returns extra fields for performance logging.
func (r *ExistenceListResults) ExtraLogFields() map[string]any { return nil }

// NewExistenceListResults creates a successful ExistenceListResults.
func NewExistenceListResults(
	streamers map[string]StreamerInfoWithStatus,
	duration time.Duration,
) *ExistenceListResults {
	return &ExistenceListResults{Streamers: streamers, duration: duration}
}

// NewExistenceListResultsFailed creates a failed ExistenceListResults.
func NewExistenceListResultsFailed() *ExistenceListResults {
	return &ExistenceListResults{failed: true}
}

// ShowKind represents the kind of show
type ShowKind int

const (
	// ShowUnknown means the show kind is unknown
	ShowUnknown ShowKind = 0
	// ShowPublic means the show is public
	ShowPublic ShowKind = 1
	// ShowGroup means the show is a group show
	ShowGroup ShowKind = 2
	// ShowTicket means the show is a ticket show
	ShowTicket ShowKind = 3
	// ShowHidden means the show is hidden
	ShowHidden ShowKind = 4
	// ShowPrivate means the show is private
	ShowPrivate ShowKind = 5
	// ShowAway means the model is away
	ShowAway ShowKind = 6
)

// String returns the symbolic name of the show kind.
func (s ShowKind) String() string {
	switch s {
	case ShowUnknown:
		return "unknown"
	case ShowPublic:
		return "public"
	case ShowGroup:
		return "group"
	case ShowTicket:
		return "ticket"
	case ShowHidden:
		return "hidden"
	case ShowPrivate:
		return "private"
	case ShowAway:
		return "away"
	}
	return fmt.Sprintf("ShowKind(%d)", int(s))
}

// StreamerInfo carries presentation data for a streamer: cover image,
// viewer count, current show kind, and the room subject when supported.
type StreamerInfo struct {
	ImageURL string   `json:"image_url,omitempty"`
	Viewers  *int     `json:"viewers,omitempty"`
	ShowKind ShowKind `json:"show_kind,omitempty"`
	Subject  string   `json:"subject,omitempty"`
}

// StreamerInfoWithStatus is StreamerInfo plus a StatusKind verdict.
type StreamerInfoWithStatus struct {
	StreamerInfo
	Status StatusKind `json:"status"`
}

// StatusUpdate represents an update of streamer status
type StatusUpdate struct {
	Nickname string
	Status   StatusKind
}
