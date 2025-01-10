package cmdlib

import (
	"context"
	"io"
)

// CloseBody closes request body
func CloseBody(body io.Closer) {
	err := body.Close()
	if err == context.Canceled {
		return
	}
	if err == context.DeadlineExceeded {
		return
	}
	CheckErr(err)
}
