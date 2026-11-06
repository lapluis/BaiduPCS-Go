//go:build !windows

package requester

func retryableDialError(error) bool { return false }
