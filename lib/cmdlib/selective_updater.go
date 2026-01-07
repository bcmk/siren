package cmdlib

// SelectiveUpdater creates an updater for selected streams
func SelectiveUpdater() Updater {
	return &selectiveUpdater{}
}

type selectiveUpdater struct {
	siteOnlineModels map[string]bool
	subscriptionSet  map[string]bool
}

func (u *selectiveUpdater) Init(config UpdaterConfig) {
	u.siteOnlineModels = config.SiteOnlineModels
	u.subscriptionSet = selectKnowns(config.SubscriptionStatuses)
}

func (u *selectiveUpdater) ProcessStreams(result StatusResults) StatusUpdateResults {
	queriedModels := result.Request.Models
	if result.Error {
		return StatusUpdateResults{Error: true}
	}

	online := onlyOnline(result.Statuses)
	unfilteredUpdates := getUpdates(u.siteOnlineModels, online)

	var updates []StatusUpdate
	for _, x := range unfilteredUpdates {
		if queriedModels[x.ModelID] {
			updates = append(updates, x)
		}
	}
	u.siteOnlineModels = online

	// Add StatusUnknown for unsubscribed models
	_, unknowns := HashDiffNewRemoved(u.subscriptionSet, queriedModels)
	u.subscriptionSet = queriedModels
	for _, m := range unknowns {
		updates = append(updates, StatusUpdate{ModelID: m, Status: StatusUnknown})
	}

	return StatusUpdateResults{
		Updates: updates,
		Images:  result.Images,
		Elapsed: result.Elapsed,
	}
}

func (u *selectiveUpdater) NeedsSubscribedModels() bool {
	return true
}

func selectKnowns(xs map[string]StatusKind) map[string]bool {
	result := map[string]bool{}
	for k, v := range xs {
		if v != StatusUnknown {
			result[k] = true
		}
	}
	return result
}
