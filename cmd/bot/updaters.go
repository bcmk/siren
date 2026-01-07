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

type onlineListUpdater struct {
	unconfirmedOnlineModels map[string]bool
}

func (f *onlineListUpdater) init(unconfirmedOnlineModels map[string]bool) {
	f.unconfirmedOnlineModels = unconfirmedOnlineModels
}

func (f *onlineListUpdater) processResults(result cmdlib.StatusResults) statusUpdateResults {
	if result.Error {
		return statusUpdateResults{err: true}
	}
	online := onlyOnline(result.Statuses)
	updates := getUpdates(f.unconfirmedOnlineModels, online)
	f.unconfirmedOnlineModels = online
	return statusUpdateResults{
		updates: updates,
		images:  result.Images,
		elapsed: result.Elapsed,
	}
}

type fixedListUpdater struct {
	unconfirmedOnlineModels map[string]bool
	subscriptionSet         map[string]bool
}

func (u *fixedListUpdater) init(unconfirmedOnlineModels map[string]bool, subscriptionStatuses map[string]cmdlib.StatusKind) {
	u.unconfirmedOnlineModels = unconfirmedOnlineModels
	u.subscriptionSet = selectKnowns(subscriptionStatuses)
}

func (u *fixedListUpdater) processResults(result cmdlib.StatusResults) statusUpdateResults {
	queriedModels := result.Request.Models
	if result.Error {
		return statusUpdateResults{err: true}
	}

	online := onlyOnline(result.Statuses)
	unfilteredUpdates := getUpdates(u.unconfirmedOnlineModels, online)

	var updates []cmdlib.StatusUpdate
	for _, x := range unfilteredUpdates {
		if queriedModels[x.ModelID] {
			updates = append(updates, x)
		}
	}
	u.unconfirmedOnlineModels = online

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
