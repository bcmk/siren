package cmdlib

type selectiveUpdater struct {
	checker          Checker
	siteOnlineModels map[string]bool
	subscriptionSet  map[string]bool
}

func (u *selectiveUpdater) Init(updaterConfig UpdaterConfig) {
	u.siteOnlineModels = updaterConfig.SiteOnlineModels
	u.subscriptionSet = selectKnowns(updaterConfig.SubscriptionStatuses)
}

func (u *selectiveUpdater) PushUpdateRequest(updateRequest StatusUpdateRequest) error {
	subscriptionStatusSet := convertToStatusSet(updateRequest.SubscriptionStatuses)
	return u.checker.PushStatusRequest(selectiveUpdateReqToStatus(updateRequest, func(res StatusResults) {
		var updateResults StatusUpdateResults
		if res.Data != nil {
			online := onlyOnline(res.Data.Statuses)
			unfilteredUpdates := getUpdates(u.siteOnlineModels, online)
			var updates []StatusUpdate
			for _, x := range unfilteredUpdates {
				if subscriptionStatusSet[x.ModelID] {
					updates = append(updates, x)
				}
			}
			u.siteOnlineModels = online
			_, unknowns := HashDiffNewRemoved(u.subscriptionSet, subscriptionStatusSet)
			u.subscriptionSet = subscriptionStatusSet
			for _, u := range unknowns {
				updates = append(updates, StatusUpdate{ModelID: u, Status: StatusUnknown})
			}
			updateResults = StatusUpdateResults{Data: &StatusUpdateResultsData{
				Updates: updates,
				Images:  res.Data.Images,
				Elapsed: res.Data.Elapsed,
			}}
			u.siteOnlineModels = online
		}
		updateResults.Errors = res.Errors
		updateRequest.Callback(updateResults)
	}))
}

func (u *selectiveUpdater) NeedsSubscriptionStatuses() bool {
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
