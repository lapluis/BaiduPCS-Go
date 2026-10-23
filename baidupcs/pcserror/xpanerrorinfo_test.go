package pcserror

import (
	"fmt"
	"strings"
	"testing"
)

func TestXPanErrorsCannotCancelEachOther(t *testing.T) {
	for _, errno := range []int{-7, 0, 1, 2, 7} {
		for _, returnType := range []int{-5, 0, 1, 2, 3, 9} {
			body := fmt.Sprintf(`{"errno":%d,"return_type":%d}`, errno, returnType)
			err := DecodeXPanJSONError("synthetic operation", strings.NewReader(body))
			if errno == 0 && returnType == 2 {
				if err != nil {
					t.Errorf("success rejected: %s: %v", body, err)
				}
				continue
			}
			if err == nil {
				t.Errorf("error combination reported success: %s", body)
				continue
			}
			if err.GetErrType() != ErrTypeRemoteError || err.GetRemoteErrCode() == 0 {
				t.Errorf("error lost remote classification: %s: %v", body, err)
			}
			if errno != 0 && err.GetRemoteErrCode() != errno {
				t.Errorf("errno changed by return_type: %s: got %d", body, err.GetRemoteErrCode())
			}
		}
	}
}

func TestXPanErrorCodeDoesNotOverflowToSuccess(t *testing.T) {
	max := int(^uint(0) >> 1)
	min := -max - 1
	for _, fields := range [][2]int{{max, min + 3}, {min, min + 2}} {
		info := &XPanErrorInfo{ErrNo: fields[0], ReturnType: fields[1]}
		if code := info.GetRemoteErrCode(); code != fields[0] {
			t.Errorf("remote errno %d was changed to %d", fields[0], code)
		}
	}
}
