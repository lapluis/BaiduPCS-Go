package requester

import (
	"errors"

	"golang.org/x/sys/windows"
)

func retryableDialError(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE)
}
