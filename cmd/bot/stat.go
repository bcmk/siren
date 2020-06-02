package main

type statistics struct {
	UsersCount                     int    `json:"users_count"`
	GroupsCount                    int    `json:"groups_count"`
	ActiveUsersOnEndpointCount     int    `json:"active_users_on_endpoint_count"`
	ActiveUsersTotalCount          int    `json:"active_users_total_count"`
	HeavyUsersCount                int    `json:"heavy_users_count"`
	ModelsCount                    int    `json:"models_count"`
	ModelsToPollOnEndpointCount    int    `json:"models_to_poll_on_endpoint_count"`
	ModelsToPollTotalCount         int    `json:"models_to_poll_total_count"`
	OnlineModelsCount              int    `json:"online_models_count"`
	QueriesDurationMilliseconds    int    `json:"queries_duration_milliseconds"`
	ErrorRate                      [2]int `json:"error_rate"`
	Rss                            int64  `json:"rss"`
	MaxRss                         int64  `json:"max_rss"`
	TransactionsOnEndpointCount    int    `json:"transactions_on_endpoint_count"`
	TransactionsOnEndpointFinished int    `json:"transactions_on_endpoint_finished"`
	UserReferralsCount             int    `json:"user_referrals_count"`
	ModelReferralsCount            int    `json:"model_referrals_count"`
	ReportsCount                   int    `json:"reports_count"`
}
