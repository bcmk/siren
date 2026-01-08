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
	unconfirmedOnlineChannels map[string]bool
}

func (f *onlineListUpdater) init(unconfirmedOnlineChannels map[string]bool) {
	f.unconfirmedOnlineChannels = unconfirmedOnlineChannels
}

func (f *onlineListUpdater) processResults(result cmdlib.StatusResults) statusUpdateResults {
	if result.Error {
		return statusUpdateResults{err: true}
	}
	online := onlyOnline(result.Statuses)
	updates := getUpdates(f.unconfirmedOnlineChannels, online)
	f.unconfirmedOnlineChannels = online
	return statusUpdateResults{
		updates: updates,
		images:  result.Images,
		elapsed: result.Elapsed,
	}
}

type fixedListUpdater struct {
	unconfirmedOnlineChannels map[string]bool
	subscriptionSet           map[string]bool
}

func (u *fixedListUpdater) init(unconfirmedOnlineChannels map[string]bool, subscriptionStatuses map[string]cmdlib.StatusKind) {
	u.unconfirmedOnlineChannels = unconfirmedOnlineChannels
	u.subscriptionSet = selectKnowns(subscriptionStatuses)
}

func (u *fixedListUpdater) processResults(result cmdlib.StatusResults) statusUpdateResults {
	queriedChannels := result.Request.Channels
	if result.Error {
		return statusUpdateResults{err: true}
	}

	online := onlyOnline(result.Statuses)
	unfilteredUpdates := getUpdates(u.unconfirmedOnlineChannels, online)

	var updates []cmdlib.StatusUpdate
	for _, x := range unfilteredUpdates {
		if queriedChannels[x.ChannelID] {
			updates = append(updates, x)
		}
	}
	u.unconfirmedOnlineChannels = online

	// Add StatusUnknown for unsubscribed channels
	_, unknowns := cmdlib.HashDiffNewRemoved(u.subscriptionSet, queriedChannels)
	u.subscriptionSet = queriedChannels
	for _, m := range unknowns {
		updates = append(updates, cmdlib.StatusUpdate{ChannelID: m, Status: cmdlib.StatusUnknown})
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
		result = append(result, cmdlib.StatusUpdate{ChannelID: i, Status: cmdlib.StatusOffline})
	}
	for _, i := range newElems {
		result = append(result, cmdlib.StatusUpdate{ChannelID: i, Status: cmdlib.StatusOnline})
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
