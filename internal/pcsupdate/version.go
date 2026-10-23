package pcsupdate

import (
	"strconv"
	"strings"
)

func newerStableRelease(current, next string) bool {
	parse := func(version string) ([3]uint64, bool) {
		var numbers [3]uint64
		if !strings.HasPrefix(version, "v") {
			return numbers, false
		}
		parts := strings.Split(version[1:], ".")
		if len(parts) != 3 {
			return numbers, false
		}
		for i, part := range parts {
			if part == "" {
				return numbers, false
			}
			for _, c := range part {
				if c < '0' || c > '9' {
					return numbers, false
				}
			}
			n, err := strconv.ParseUint(part, 10, 64)
			if err != nil {
				return numbers, false
			}
			numbers[i] = n
		}
		return numbers, true
	}
	base, prerelease, hasPrerelease := strings.Cut(current, "-")
	a, ok := parse(base)
	if !ok {
		return false
	}
	b, ok := parse(next)
	if !ok {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return b[i] > a[i]
		}
	}
	return hasPrerelease && prerelease != ""
}
