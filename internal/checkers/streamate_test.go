package checkers

import (
	"encoding/xml"
	"testing"
)

// A performer's image is the biopic an answer includes, and none where it includes no biopic.
func TestStreamateImageURL(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"with a biopic",
			`<Performer Name="InnaWilliams" StreamType="live"><Media><Pic>` +
				`<Full Src="//imagetransform.icfcdn.com/avatar/InnaWilliams.jpeg"/></Pic></Media></Performer>`,
			"https://imagetransform.icfcdn.com/avatar/InnaWilliams.jpeg"},
		{"without one", `<Performer Name="InnaWilliams" StreamType="live"/>`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m performerResponse
			if err := xml.Unmarshal([]byte(tc.body), &m); err != nil {
				t.Fatal(err)
			}
			if got := streamateImageURL(m); got != tc.want {
				t.Errorf("image = %q, want %q", got, tc.want)
			}
		})
	}
}
