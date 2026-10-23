package pcsdownload

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestDownloadRetryClassifiesRemoteCodesAndWrappedErrors(t *testing.T) {
	remote := func(code int) error {
		return pcserror.DecodePCSJSONError("synthetic download", strings.NewReader(fmt.Sprintf(`{"error_code":%d,"error_msg":"synthetic"}`, code)))
	}
	network := func(cause error) error {
		info := pcserror.NewPCSErrorInfo("synthetic")
		info.SetNetError(cause)
		return info
	}
	for _, tc := range []struct {
		name  string
		err   error
		retry bool
	}{
		{"missing_user", remote(31045), false},
		{"missing_file", remote(31066), false},
		{"missing_file_alternative", remote(31297), false},
		{"retryable_remote", remote(31626), true},
		{"unknown_remote", remote(99999), true},
		{"wrapped_missing_file", fmt.Errorf("download: %w", remote(31066)), false},
		{"wrapped_retryable_remote", fmt.Errorf("download: %w", remote(31626)), true},
		{"local_path_error", &os.PathError{Op: "create", Path: "synthetic", Err: os.ErrPermission}, false},
		{"wrapped_local_error", fmt.Errorf("initialize: %w", &os.PathError{Op: "create", Path: "synthetic", Err: os.ErrPermission}), false},
		{"canceled", context.Canceled, false},
		{"wrapped_canceled", fmt.Errorf("download: %w", context.Canceled), false},
		{"pcs_canceled", network(context.Canceled), false},
		{"pcs_path_error", network(&os.PathError{Op: "open", Path: "synthetic", Err: os.ErrPermission}), false},
		{"pcs_network_error", network(errors.New("synthetic timeout")), true},
		{"deadline", context.DeadlineExceeded, true},
		{"other", errors.New("synthetic transient failure"), true},
		{"no_error", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, initial := range []bool{false, true} {
				result := &taskframework.TaskUnitRunResult{Err: tc.err, NeedRetry: initial}
				(&DownloadTaskUnit{}).handleError(result)
				if result.NeedRetry != tc.retry {
					t.Errorf("initial=%v retry=%v want=%v", initial, result.NeedRetry, tc.retry)
				}
			}
		})
	}
}

func TestDownloadInitializationPreservesLocalError(t *testing.T) {
	for _, targetIsDirectory := range []bool{false, true} {
		dir := t.TempDir()
		target := dir
		if !targetIsDirectory {
			parent := filepath.Join(dir, "file-parent")
			if err := os.WriteFile(parent, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			target = filepath.Join(parent, "output")
		}
		unit := emptyTask(target, false, false)
		err := unit.download("", requester.NewHTTPClient())
		var pathErr *os.PathError
		if !errors.As(err, &pathErr) {
			t.Fatalf("initialization lost path error: %v", err)
		}
		result := &taskframework.TaskUnitRunResult{Err: err}
		unit.handleError(result)
		if result.NeedRetry {
			t.Fatalf("permanent initialization error retried: %v", err)
		}
	}
}
