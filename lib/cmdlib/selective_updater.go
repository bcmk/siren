package cmdlib

type selectiveUpdater struct {
	checker          Checker
	siteOnlineModels map[string]bool
	knowns           map[string]bool
}

func (f *selectiveUpdater) PushUpdateRequest(updateRequest StatusUpdateRequest) error {
	subsSet := subscriptionsSet(updateRequest.Subscriptions)
	return f.checker.PushStatusRequest(selectiveUpdateReqToStatus(updateRequest, func(res StatusResults) {
		var updateResults StatusUpdateResults
		if res.Data != nil {
			online := onlyOnline(res.Data.Statuses)
			updates := getUpdates(f.siteOnlineModels, online)
			f.siteOnlineModels = online
			_, unknowns := HashDiffNewRemoved(f.knowns, subsSet)
			f.knowns = subsSet
			for _, u := range unknowns {
				updates = append(updates, StatusUpdate{ModelID: u, Status: StatusUnknown})
			}
			updateResults = StatusUpdateResults{Data: &StatusUpdateResultsData{
				Updates: updates,
				Images:  res.Data.Images,
				Elapsed: res.Data.Elapsed,
			}}
			f.siteOnlineModels = online
		}
		updateResults.Errors = res.Errors
		updateRequest.Callback(updateResults)
	}))
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
