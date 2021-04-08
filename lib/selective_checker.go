package lib

import (
	"time"
)

// SelectiveChecker retrieves a full list of online models
type SelectiveChecker interface {
	Checker
	CheckMany(subscriptions []string) (onlineModels map[string]bool, images map[string]string, err error)
}

// SelectiveCheckerCommon contains common fields for a full checker
type SelectiveCheckerCommon struct {
	CheckerCommon
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

func setToSlice(xs map[string]bool) []string {
	result := make([]string, len(xs))
	i := 0
	for k := range xs {
		result[i] = k
		i++
	}
	return result
}

func selectiveDaemonStart(c SelectiveChecker, siteOnlineModels map[string]bool, subscriptions map[string]StatusKind, dbg bool) (
	statusRequests chan StatusRequest,
	resultsCh chan CheckerResults,
	errorsCh chan struct{},
	elapsedCh chan time.Duration,
) {
	knowns := selectKnowns(subscriptions)
	statusRequests = make(chan StatusRequest)
	resultsCh = make(chan CheckerResults)
	errorsCh = make(chan struct{})
	elapsedCh = make(chan time.Duration)
	go func() {
	requests:
		for request := range statusRequests {
			start := time.Now()
			subsSet := subscriptionsSet(request.Subscriptions)
			nextOnline, images, err := c.CheckMany(setToSlice(subsSet))
			if err != nil {
				Lerr("%v", err)
				errorsCh <- struct{}{}
				continue requests
			}
			if dbg {
				Ldbg("online models for endpoint: %d", len(nextOnline))
			}
			elapsedCh <- time.Since(start)
			if dbg {
				Ldbg("online models: %d", len(nextOnline))
			}
			updates := getUpdates(siteOnlineModels, nextOnline)
			siteOnlineModels = nextOnline
			_, unknowns := HashDiffNewRemoved(knowns, subsSet)
			knowns = subsSet
			for _, u := range unknowns {
				updates = append(updates, StatusUpdate{ModelID: u, Status: StatusUnknown})
			}
			resultsCh <- CheckerResults{Updates: updates, Images: images}
		}
	}()
	return
}
