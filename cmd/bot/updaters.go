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

type fixedListUpdater struct{}

func (u *fixedListUpdater) processResults(
	result cmdlib.StatusResults,
	lastOnline map[string]cmdlib.ChannelInfo,
) map[string]cmdlib.ChannelInfo {
	candidateOnline := filterOnline(result.Channels)
	return getUpdates(lastOnline, candidateOnline)
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
