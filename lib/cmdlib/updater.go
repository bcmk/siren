package cmdlib

import "time"

// Updater implements updater adapter
type Updater interface {
	Init(updaterConfig UpdaterConfig)
	PushUpdateRequest(updateRequest StatusUpdateRequest) error
	NeedsSubscriptionStatuses() bool
}

// StatusUpdateRequest represents a request of models updates
type StatusUpdateRequest struct {
	SubscriptionStatuses map[string]StatusKind
	Specific             map[string]bool
	Callback             func(StatusUpdateResults)
}

// StatusUpdate represents an update of model status
type StatusUpdate struct {
	ModelID string
	Status  StatusKind
}

// StatusUpdateResultsData contains data from updates checking algorithm
type StatusUpdateResultsData struct {
	Updates []StatusUpdate
	Images  map[string]string
	Elapsed time.Duration
}

// StatusUpdateResults contains results from updates checking algorithm
type StatusUpdateResults struct {
	Data   *StatusUpdateResultsData
	Errors int
}

func getUpdates(prev, next map[string]bool) []StatusUpdate {
	var result []StatusUpdate
	newElems, removed := HashDiffNewRemoved(prev, next)
	for _, i := range removed {
		result = append(result, StatusUpdate{ModelID: i, Status: StatusOffline})
	}
	for _, i := range newElems {
		result = append(result, StatusUpdate{ModelID: i, Status: StatusOnline})
	}
	return result
}

func fullUpdateReqToStatus(r StatusUpdateRequest, callback func(StatusResults)) StatusRequest {
	var specific map[string]bool
	if r.Specific != nil {
		specific = map[string]bool{}
		for k := range r.Specific {
			specific[k] = true
		}
	}
	return StatusRequest{
		Specific: specific,
		Callback: callback,
	}
}

func selectiveUpdateReqToStatus(r StatusUpdateRequest, callback func(StatusResults)) StatusRequest {
	specific := map[string]bool{}
	for k := range r.SubscriptionStatuses {
		specific[k] = true
	}
	for k := range r.Specific {
		specific[k] = true
	}
	return StatusRequest{
		Specific: specific,
		Callback: callback,
	}
}

func onlyOnline(ss map[string]StatusKind) map[string]bool {
	boolMap := map[string]bool{}
	for k, s := range ss {
		if s == StatusOnline {
			boolMap[k] = true
		}
	}
	return boolMap
}

func onlineStatuses(ss map[string]bool) map[string]StatusKind {
	statusMap := map[string]StatusKind{}
	for k, s := range ss {
		if s {
			statusMap[k] = StatusOnline
		}
	}
	return statusMap
}
