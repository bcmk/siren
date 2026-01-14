package main

import "github.com/bcmk/siren/lib/cmdlib"

func filterOnline(channels map[string]cmdlib.ChannelInfo) map[string]cmdlib.ChannelInfo {
	result := map[string]cmdlib.ChannelInfo{}
	for k, info := range channels {
		if info.Status == cmdlib.StatusOnline {
			result[k] = info
		}
	}
	return result
}
