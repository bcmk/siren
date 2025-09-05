package main

type statistics struct {
	QueriesDurationMilliseconds int    `json:"queries_duration_milliseconds"`
	UpdatesDurationMilliseconds int    `json:"updates_duration_milliseconds"`
	ErrorRate                   [2]int `json:"error_rate"`
	DownloadErrorRate           [2]int `json:"download_error_rate"`
	Rss                         int64  `json:"rss"`
	ChangesInPeriod             int    `json:"changes_in_period"`
	ConfirmedChangesInPeriod    int    `json:"confirmed_changes_in_period"`
}
