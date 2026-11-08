package netdisksign_test

import (
	"math"
	"net/url"
	"strconv"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/netdisksign"
)

func TestLocateDownloadSign(t *testing.T) {
	for _, tc := range []struct {
		name, device, credential, want string
		time                           int64
		uid                            uint64
	}{
		{"legacy", "O|1E67351CCE80B2CF48DB511CD77ACD9F", "test_bduss", "b6bb7a6f46899e181baea58798d4fdb889775c2c", 1571140066, 10086},
		// Boundary digests were independently calculated with .NET SHA1 over
		// the complete protocol input, using only synthetic credentials.
		{"large-id", "fixture|0", "fixture-credential", "1c0c9bea3e217e4d30b2499276c602ae0addc8e2", 2147483648, 9007199254740993},
		{"max-values", "fixture|0", "fixture-credential", "bff44463bfd1af1b33e5d91b17c228f535ae93c1", math.MaxInt64, math.MaxUint64},
		{"negative-time", "fixture|0", "fixture-credential", "42d3f2b4a9ef68516dd9d9504fd9b6b7fa83edc4", math.MinInt64, 4294967296},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sign := netdisksign.NewLocateDownloadSignWithTimeAndDevUID(tc.time, tc.device, tc.uid, tc.credential)
			if sign.Rand != tc.want || sign.Time != tc.time || sign.DevUID != tc.device {
				t.Fatalf("signature mismatch: got=%+v want digest=%s", sign, tc.want)
			}
			query, err := url.ParseQuery(sign.URLParam())
			if err != nil {
				t.Fatal(err)
			}
			timestamp, err := strconv.ParseInt(query.Get("time"), 10, 64)
			if err != nil || timestamp != tc.time || query.Get("rand") != tc.want || query.Get("devuid") != tc.device || query.Get("cuid") != tc.device || len(query) != 4 {
				t.Fatalf("signature URL parameters changed: %v err=%v", query, err)
			}
		})
	}
}
