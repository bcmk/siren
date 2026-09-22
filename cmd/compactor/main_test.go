package main

import "testing"

func TestFormatLags(t *testing.T) {
	tests := []struct {
		name string
		lags []int
		want string
	}{
		{name: "caught up", lags: []int{0, 0, 0}, want: "[0 0 0]"},
		{name: "lagging", lags: []int{0, 5 * day, 59*day + 3*hour + 59*60}, want: "[0 5d0h0m0s 59d3h59m0s]"},
		{name: "under an hour", lags: []int{900, 59, 0}, want: "[0d0h15m0s 0d0h0m59s 0]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatLags(tt.lags); got != tt.want {
				t.Errorf("formatLags = %q, want %q", got, tt.want)
			}
		})
	}
}
