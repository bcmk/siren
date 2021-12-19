package lib

type fullUpdater struct {
	checker          Checker
	siteOnlineModels map[string]bool
}

func (f *fullUpdater) QueryUpdates(updateRequest StatusUpdateRequest) error {
	return f.checker.QueryStatuses(fullUpdateReqToStatus(updateRequest, func(res StatusResults) {
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
