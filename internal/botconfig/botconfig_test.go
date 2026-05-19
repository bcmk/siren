package botconfig

import "testing"

func TestValidateSubsTiers(t *testing.T) {
	tests := []struct {
		name    string
		in      []SubsTier
		wantErr bool
	}{
		{
			name: "empty is valid (disabled)",
			in:   nil,
		},
		{
			name: "valid custom tiers",
			in:   []SubsTier{{Count: 10, Cost: 500}, {Count: 100, Cost: 3000}},
		},
		{
			name: "single tier",
			in:   []SubsTier{{Count: 10, Cost: 500}},
		},
		{
			name: "equal price is allowed",
			in:   []SubsTier{{Count: 10, Cost: 500}, {Count: 20, Cost: 1000}},
		},
		{
			name:    "zero count",
			in:      []SubsTier{{Count: 0, Cost: 500}},
			wantErr: true,
		},
		{
			name:    "negative count",
			in:      []SubsTier{{Count: -1, Cost: 500}},
			wantErr: true,
		},
		{
			name:    "zero price",
			in:      []SubsTier{{Count: 10, Cost: 0}},
			wantErr: true,
		},
		{
			name:    "equal count not ascending",
			in:      []SubsTier{{Count: 10, Cost: 500}, {Count: 10, Cost: 400}},
			wantErr: true,
		},
		{
			name:    "descending count",
			in:      []SubsTier{{Count: 20, Cost: 1000}, {Count: 10, Cost: 400}},
			wantErr: true,
		},
		{
			name:    "increasing price",
			in:      []SubsTier{{Count: 10, Cost: 300}, {Count: 20, Cost: 800}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSubsTiers(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}
