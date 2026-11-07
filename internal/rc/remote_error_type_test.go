package rc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestMissingRemoteCodeCannotHideLocalErrors(t *testing.T) {
	for _, code := range []int{31066, -9} {
		for _, kind := range []string{"remote", "cancel", "timeout", "network", "json", "other"} {
			t.Run(fmt.Sprintf("%d/%s", code, kind), func(t *testing.T) {
				cause := errors.New("fixture response failure")
				info := pcserror.NewPCSErrorInfo("fixture metadata")
				info.ErrCode = code
				var want error
				switch kind {
				case "remote":
					info.SetRemoteError()
					want = os.ErrNotExist
				case "cancel":
					info.SetNetError(context.Canceled)
					want = context.Canceled
				case "timeout":
					info.SetNetError(context.DeadlineExceeded)
					want = context.DeadlineExceeded
				case "network":
					info.SetNetError(cause)
					want = cause
				case "json":
					info.SetJSONError(cause)
					want = cause
				case "other":
					info.ErrType = pcserror.ErrTypeOthers
					info.Err = cause
					want = cause
				}
				got := remoteError(info)
				if !errors.Is(got, want) || os.IsNotExist(got) != (kind == "remote") {
					t.Fatal("wrong missing-file classification", got)
				}
			})
		}
	}
}
