package rc

import (
	"context"
	"os"
	"testing"
)

func TestInitialMkdirFailureLeavesAllFileActionsPending(t *testing.T) {
	for _, mode := range []string{"copy", "sync", "move"} {
		t.Run(mode, func(t *testing.T) {
			source, destination := endpoints(t)
			write(t, source, "nested/file", "new content")
			write(t, source, "same", "unchanged")
			write(t, destination, "same", "unchanged")
			write(t, destination, "extra", "retain until successful sync")
			options := Options{SizeOnly: true, CreateEmptyDirs: true, MaxDelete: -1, Transfers: 2}
			plan, err := BuildPlan(context.Background(), source, destination, mode, options)
			if err != nil {
				t.Fatal(err)
			}
			fileActions, directoryActions := 0, 0
			for _, action := range plan.Actions {
				if action.Kind == "copy" || action.Kind == "skip" {
					fileActions++
				}
				if action.Kind == "mkdir" && action.Path == "nested" {
					directoryActions++
				}
			}
			if fileActions != 2 || directoryActions != 1 {
				t.Fatalf("fixture did not plan two file actions and a directory: %+v", plan.Actions)
			}
			// A file appears where a directory was planned, before execution.
			write(t, destination, "nested", "preserve occupying file")
			result, err := Execute(context.Background(), plan, options)
			if err == nil || result != (Result{Pending: 2}) {
				t.Errorf("directory preparation hid pending work: result=%+v err=%v", result, err)
			}
			for _, expected := range []struct {
				endpoint   Endpoint
				path, data string
			}{
				{source, "nested/file", "new content"},
				{source, "same", "unchanged"},
				{destination, "same", "unchanged"},
				{destination, "extra", "retain until successful sync"},
				{destination, "nested", "preserve occupying file"},
			} {
				if data, err := os.ReadFile(expected.endpoint.At(expected.path)); err != nil || string(data) != expected.data {
					t.Errorf("preparation failure changed %s: data=%q err=%v", expected.path, data, err)
				}
			}
		})
	}
}
