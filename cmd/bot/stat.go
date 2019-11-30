package main

type statistics struct {
	UsersCount        int `json:"users_count"`
	ActiveUsersCount  int `json:"active_users_count"`
	ModelsCount       int `json:"models_count"`
	ActiveModelsCount int `json:"active_models_count"`
}
