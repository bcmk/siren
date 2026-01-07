package cmdlib

import "time"

// UpdaterConfig represents updater config
type UpdaterConfig struct {
	SiteOnlineModels     map[string]bool
	SubscriptionStatuses map[string]StatusKind
}

// Updater processes raw stream results into status updates
type Updater interface {
	Init(config UpdaterConfig)
	ProcessStreams(result StatusResults) StatusUpdateResults
	NeedsSubscribedModels() bool
}

// StatusUpdate represents an update of model status
type StatusUpdate struct {
	ModelID string
	Status  StatusKind
}

// StatusUpdateResults contains results from updates checking algorithm
type StatusUpdateResults struct {
	Updates []StatusUpdate
	Images  map[string]string
	Elapsed time.Duration
	Error   bool
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
