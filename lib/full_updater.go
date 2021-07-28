package lib

type fullUpdater struct {
	checker          Checker
	siteOnlineModels map[string]bool
}

func (f *fullUpdater) QueryUpdates(updateRequest StatusUpdateRequest) error {
	return f.checker.QueryStatuses(fullUpdateReqToStatus(updateRequest, func(res StatusResults) {
		if res.Data != nil {
			online := onlyOnline(res.Data.Statuses)
			updateRequest.Callback(StatusUpdateResults{
				Data: &StatusUpdateResultsData{
					Updates: getUpdates(f.siteOnlineModels, online),
					Images:  res.Data.Images,
					Elapsed: res.Data.Elapsed,
				},
				Errors: res.Errors})
			f.siteOnlineModels = online
		} else {
			updateRequest.Callback(StatusUpdateResults{Errors: res.Errors})
		}
	}))
}
