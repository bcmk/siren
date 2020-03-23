package payments

import (
	"encoding/json"
)

// CoinPayments API result is nonhomogeneous
// It can be empty array or an object as result
type transactionWrapper struct{ *Transaction }

func (w *transactionWrapper) UnmarshalJSON(b []byte) error {
	if b[0] == '[' {
		var a []interface{}
		if err := json.Unmarshal(b, &a); err != nil {
			return err
		}
		*w = transactionWrapper{}
		return nil
	}
	var t Transaction
	if err := json.Unmarshal(b, &t); err != nil {
		return err
	}
	*w = transactionWrapper{&t}
	return nil
}
