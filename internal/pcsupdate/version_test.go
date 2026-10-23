package pcsupdate

import "testing"

func TestNewerStableRelease(t *testing.T) {
	for _, tc := range []struct {
		current, next string
		want          bool
	}{
		{"v3.9.9", "v3.10.0", true}, {"v3.10.0", "v3.9.9", false},
		{"v4.0.2-dev", "v4.0.2", true}, {"v4.0.2", "v4.0.2", false},
		{"v4.0.2", "v5.0.0", true}, {"v4.0.2", "v5.0.0-Beta", false},
		{"v4.0.2", "v5.bad.0", false}, {"v4.0.2", "", false},
	} {
		if got := newerStableRelease(tc.current, tc.next); got != tc.want {
			t.Errorf("%s -> %s: %v, want %v", tc.current, tc.next, got, tc.want)
		}
	}
}
