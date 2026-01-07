package main

import (
	"time"

	"github.com/bcmk/siren/lib/cmdlib"
)

type statusUpdateResults struct {
	updates []cmdlib.StatusUpdate
	images  map[string]string
	elapsed time.Duration
	err     bool
}

type fullUpdater struct {
	siteOnlineModels map[string]bool
}

func (f *fullUpdater) init(siteOnlineModels map[string]bool) {
	f.siteOnlineModels = siteOnlineModels
}

func (f *fullUpdater) processStreams(result cmdlib.StatusResults) statusUpdateResults {
	if result.Error {
		return statusUpdateResults{err: true}
	}
	online := onlyOnline(result.Statuses)
	updates := getUpdates(f.siteOnlineModels, online)
	f.siteOnlineModels = online
	return statusUpdateResults{
		updates: updates,
		images:  result.Images,
		elapsed: result.Elapsed,
	}
}

type selectiveUpdater struct {
	siteOnlineModels map[string]bool
	subscriptionSet  map[string]bool
}

func (u *selectiveUpdater) init(siteOnlineModels map[string]bool, subscriptionStatuses map[string]cmdlib.StatusKind) {
	u.siteOnlineModels = siteOnlineModels
	u.subscriptionSet = selectKnowns(subscriptionStatuses)
}

func (u *selectiveUpdater) processStreams(result cmdlib.StatusResults) statusUpdateResults {
	queriedModels := result.Request.Models
	if result.Error {
		return statusUpdateResults{err: true}
	}

	online := onlyOnline(result.Statuses)
	unfilteredUpdates := getUpdates(u.siteOnlineModels, online)

	var updates []cmdlib.StatusUpdate
	for _, x := range unfilteredUpdates {
		if queriedModels[x.ModelID] {
			updates = append(updates, x)
		}
	}
	u.siteOnlineModels = online

	// Add StatusUnknown for unsubscribed models
	_, unknowns := cmdlib.HashDiffNewRemoved(u.subscriptionSet, queriedModels)
	u.subscriptionSet = queriedModels
	for _, m := range unknowns {
		updates = append(updates, cmdlib.StatusUpdate{ModelID: m, Status: cmdlib.StatusUnknown})
	}

	return statusUpdateResults{
		updates: updates,
		images:  result.Images,
		elapsed: result.Elapsed,
	}
}

func getUpdates(prev, next map[string]bool) []cmdlib.StatusUpdate {
	var result []cmdlib.StatusUpdate
	newElems, removed := cmdlib.HashDiffNewRemoved(prev, next)
	for _, i := range removed {
		result = append(result, cmdlib.StatusUpdate{ModelID: i, Status: cmdlib.StatusOffline})
	}
	for _, i := range newElems {
		result = append(result, cmdlib.StatusUpdate{ModelID: i, Status: cmdlib.StatusOnline})
	}
	return result
}

func onlyOnline(ss map[string]cmdlib.StatusKind) map[string]bool {
	boolMap := map[string]bool{}
	for k, s := range ss {
		if s == cmdlib.StatusOnline {
			boolMap[k] = true
		}
	}
	return boolMap
}

func selectKnowns(xs map[string]cmdlib.StatusKind) map[string]bool {
	result := map[string]bool{}
	for k, v := range xs {
		if v != cmdlib.StatusUnknown {
			result[k] = true
		}
	}
	return result
}
