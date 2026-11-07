package rc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsdownload"
)

func TestCanceledResumeInspectionPreservesExistingState(t *testing.T) {
	for _, phase := range []string{"record", "payload", "ranges"} {
		for _, changed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/changed=%t", phase, changed), func(t *testing.T) {
				f := newStageReadFixture(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				previousRead, previousStat := readDownloadRecord, statDownloadState
				defer func() { readDownloadRecord, statDownloadState = previousRead, previousStat }()
				if phase == "record" {
					readDownloadRecord = func(name string) ([]byte, error) {
						data, err := previousRead(name)
						cancel()
						return data, err
					}
				} else {
					selected := f.stage.payload
					if phase == "ranges" {
						selected += pcsdownload.DownloadSuffix
					}
					statDownloadState = func(name string) (os.FileInfo, error) {
						info, err := previousStat(name)
						if name == selected {
							cancel()
						}
						return info, err
					}
				}
				entry := f.entry
				if changed {
					entry.ID = "changed-remote-entity"
				}
				_, err := stageFor(ctx, f.remote, "/file", f.target, entry)
				if !errors.Is(err, context.Canceled) {
					t.Errorf("canceled inspection continued: %v", err)
				}
				readDownloadRecord, statDownloadState = previousRead, previousStat
				f.checkPreservedAndRetry(t)
			})
		}
	}
}

func TestResumeInspectionPreservesIOErrorAndCancellation(t *testing.T) {
	for _, phase := range []string{"record", "payload", "ranges"} {
		t.Run(phase, func(t *testing.T) {
			f := newStageReadFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			previousRead, previousStat := readDownloadRecord, statDownloadState
			defer func() { readDownloadRecord, statDownloadState = previousRead, previousStat }()
			cause := &os.PathError{Op: "inspect", Path: "fixture-state", Err: os.ErrPermission}
			if phase == "record" {
				readDownloadRecord = func(string) ([]byte, error) {
					cancel()
					return nil, cause
				}
			} else {
				selected := f.stage.payload
				if phase == "ranges" {
					selected += pcsdownload.DownloadSuffix
				}
				statDownloadState = func(name string) (os.FileInfo, error) {
					if name == selected {
						cancel()
						return nil, cause
					}
					return previousStat(name)
				}
			}
			_, err := stageFor(ctx, f.remote, "/file", f.target, f.entry)
			if !errors.Is(err, cause) || !errors.Is(err, os.ErrPermission) || !errors.Is(err, context.Canceled) {
				t.Errorf("inspection error or cancellation lost: %v", err)
			}
			readDownloadRecord, statDownloadState = previousRead, previousStat
			f.checkPreservedAndRetry(t)
		})
	}
}
