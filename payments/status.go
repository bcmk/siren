package payments

// StatusKind represents a status of a payment
type StatusKind int

// Payment statuses
const (
	StatusUnknown StatusKind = iota
	StatusCreated
	StatusCanceled
	StatusFinished
)

func (s StatusKind) String() string {
	switch s {
	case StatusCreated:
		return "created"
	case StatusCanceled:
		return "canceled"
	case StatusFinished:
		return "finished"
	}
	return "unknown"
}
