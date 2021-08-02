package payments

import (
	"encoding/json"
	"strconv"
)

// dest_tag is a number, but probably it can be a string as well
type flex string

func (f *flex) UnmarshalJSON(b []byte) error {
	if b[0] != '"' {
		var i int
		if err := json.Unmarshal(b, &i); err != nil {
			return err
		}
		*f = flex(strconv.Itoa(i))
		return nil
	}
	if err := json.Unmarshal(b, (*string)(f)); err != nil {
		return err
	}
	return nil
}
