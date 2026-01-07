package cmdlib

// FullUpdater creates an updater for querying all streams
func FullUpdater() Updater {
	return &fullUpdater{}
}

type fullUpdater struct {
	siteOnlineModels map[string]bool
}

func (f *fullUpdater) Init(config UpdaterConfig) {
	f.siteOnlineModels = config.SiteOnlineModels
}

func (f *fullUpdater) ProcessStreams(result StatusResults) StatusUpdateResults {
	if result.Error {
		return StatusUpdateResults{Error: true}
	}
	online := onlyOnline(result.Statuses)
	updates := getUpdates(f.siteOnlineModels, online)
	f.siteOnlineModels = online
	return StatusUpdateResults{
		Updates: updates,
		Images:  result.Images,
		Elapsed: result.Elapsed,
	}
}

func (f *fullUpdater) NeedsSubscribedModels() bool {
	return false
}
