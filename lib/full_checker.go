package lib

import (
	"time"
)

// FullChecker retrieves a full list of online models
type FullChecker interface {
	Checker
	CheckFull() (map[string]bool, map[string]string, error)
}

func fullDaemonStart(c FullChecker, siteOnlineModels map[string]bool, intervalMs int, dbg bool) (
	statusRequests chan StatusRequest,
	resultsCh chan CheckerResults,
	errorsCh chan struct{},
	elapsedCh chan time.Duration,
) {
	statusRequests = make(chan StatusRequest)
	resultsCh = make(chan CheckerResults)
	errorsCh = make(chan struct{})
	elapsedCh = make(chan time.Duration)
	go func() {
	requests:
		for request := range statusRequests {
			start := time.Now()
			nextOnline, images, err := c.CheckFull()
			if err != nil {
				Lerr("%v", err)
				errorsCh <- struct{}{}
				continue requests
			}
			for modelID := range request.SpecialModels {
				time.Sleep(time.Duration(intervalMs) * time.Millisecond)
				status := c.CheckSingle(modelID)
				if status == StatusOnline {
					nextOnline[modelID] = true
				} else if status == StatusUnknown || status == StatusNotFound {
					Lerr("status for model %s reported: %v", modelID, status)
					errorsCh <- struct{}{}
				}
			}
			elapsedCh <- time.Since(start)
			if dbg {
				Ldbg("online models: %d", len(nextOnline))
			}
			resultsCh <- CheckerResults{Updates: getUpdates(siteOnlineModels, nextOnline), Images: images}
			siteOnlineModels = nextOnline
		}
	}()
	return
}
