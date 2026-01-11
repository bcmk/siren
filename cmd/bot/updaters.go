package main

import "github.com/bcmk/siren/lib/cmdlib"

type onlineListUpdater struct{}

func (u *onlineListUpdater) processResults(
	result cmdlib.StatusResults,
	lastOnline map[string]cmdlib.ChannelInfo,
) map[string]cmdlib.ChannelInfo {
	candidateOnline := filterOnline(result.Channels)
	return getUpdates(lastOnline, candidateOnline)
}

type fixedListUpdater struct {
	subscriptionSet map[string]bool
}

func (u *fixedListUpdater) init(subscriptionStatuses map[string]cmdlib.StatusKind) {
	u.subscriptionSet = filterKnown(subscriptionStatuses)
}

func (u *fixedListUpdater) processResults(
	result cmdlib.StatusResults,
	lastOnline map[string]cmdlib.ChannelInfo,
) map[string]cmdlib.ChannelInfo {
	candidateOnline := filterOnline(result.Channels)
	updates := getUpdates(lastOnline, candidateOnline)

	// Add StatusUnknown for unsubscribed channels
	for k := range u.subscriptionSet {
		if !result.Request.Channels[k] {
			updates[k] = cmdlib.ChannelInfo{Status: cmdlib.StatusUnknown}
		}
	}
	u.subscriptionSet = result.Request.Channels
	return updates
}

func filterOnline(channels map[string]cmdlib.ChannelInfo) map[string]cmdlib.ChannelInfo {
	result := map[string]cmdlib.ChannelInfo{}
	for k, info := range channels {
		if info.Status == cmdlib.StatusOnline {
			result[k] = info
		}
	}
	return result
}

func getUpdates(
	lastOnline map[string]cmdlib.ChannelInfo,
	candidateOnline map[string]cmdlib.ChannelInfo,
) map[string]cmdlib.ChannelInfo {
	result := map[string]cmdlib.ChannelInfo{}
	for k := range lastOnline {
		if _, ok := candidateOnline[k]; !ok {
			result[k] = cmdlib.ChannelInfo{Status: cmdlib.StatusOffline}
		}
	}
	for k, info := range candidateOnline {
		if _, ok := lastOnline[k]; !ok {
			result[k] = info
		}
	}
	return result
}

func filterKnown(xs map[string]cmdlib.StatusKind) map[string]bool {
	result := map[string]bool{}
	for k, v := range xs {
		if v != cmdlib.StatusUnknown {
			result[k] = true
		}
	}
	return result
}
