package main

type statistics struct {
	UsersCount             int    `json:"users_count"`
	ActiveUsersCount       int    `json:"active_users_count"`
	ModelsCount            int    `json:"models_count"`
	ActiveModelsCount      int    `json:"active_models_count"`
	OnlineModelsCount      int    `json:"online_models_count"`
	QueriesDurationSeconds int    `json:"queries_duration"`
	ErrorRate              [2]int `json:"error_rate"`
	MemoryUsage            uint64 `json:"memory_usage"`
}
