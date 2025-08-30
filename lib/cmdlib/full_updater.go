package cmdlib

type fullUpdater struct {
	checker          Checker
	siteOnlineModels map[string]bool
}

func (f *fullUpdater) Init(updaterConfig UpdaterConfig) {
	f.siteOnlineModels = updaterConfig.SiteOnlineModels
}

func (f *fullUpdater) PushUpdateRequest(updateRequest StatusUpdateRequest) error {
	return f.checker.PushStatusRequest(fullUpdateReqToStatus(updateRequest, func(res StatusResults) {
		var updateResults StatusUpdateResults
		if res.Data != nil {
			online := onlyOnline(res.Data.Statuses)
			updateResults = StatusUpdateResults{Data: &StatusUpdateResultsData{
				Updates: getUpdates(f.siteOnlineModels, online),
				Images:  res.Data.Images,
				Elapsed: res.Data.Elapsed,
			}}
			f.siteOnlineModels = online
		}
		updateResults.Errors = res.Errors
		updateRequest.Callback(updateResults)
	}))
}

func (f *fullUpdater) NeedsSubscriptionStatuses() bool {
	return false
}
