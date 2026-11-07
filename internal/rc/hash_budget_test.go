package rc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func hashBudgetFixture(t *testing.T, count int) (Endpoint, Endpoint) {
	t.Helper()
	d := Endpoint{&Local{}, t.TempDir()}
	files := map[string]bool{}
	var listing []any
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("%02d", i)
		files["/Temp/"+name] = true
		listing = append(listing, map[string]any{"path": "/Temp/" + name, "server_filename": name, "server_mtime": 1, "fs_id": 100 + i, "size": 4})
		write(t, d, name, "data")
	}
	client := requester.NewHTTPClient()
	client.Client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		result := map[string]any{"errno": 0, "error_code": 0}
		if strings.HasSuffix(r.URL.Path, "/api/list") {
			result["list"] = listing
		} else {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				return nil, err
			}
			if r.MultipartForm != nil {
				defer r.MultipartForm.RemoveAll()
			}
			var param struct {
				List []struct {
					Path string `json:"path"`
				} `json:"list"`
			}
			if err := json.Unmarshal([]byte(r.FormValue("param")), &param); err != nil {
				return nil, err
			}
			if len(param.List) != 1 {
				return nil, fmt.Errorf("unexpected metadata request")
			}
			p := param.List[0].Path
			switch r.URL.Query().Get("method") {
			case "meta":
				if p == "/Temp" {
					result["list"] = []any{map[string]any{"path": p, "isdir": 1, "fs_id": 1}}
				} else if files[p] {
					n, err := strconv.Atoi(strings.TrimPrefix(p, "/Temp/"))
					if err != nil {
						return nil, err
					}
					result["list"] = []any{map[string]any{"path": p, "mtime": 1, "size": 4, "fs_id": 100 + n}}
				} else {
					result["error_code"] = 31066
				}
			case "delete":
				delete(files, p)
			default:
				return nil, fmt.Errorf("unexpected budget API")
			}
		}
		body, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
	})
	return Endpoint{&Remote{UID: 7, PCS: baidupcs.NewPCSWithClient(1, client)}, "/Temp"}, d
}

func TestParallelHashComparisonRespectsConnectionBudget(t *testing.T) {
	for _, tc := range []struct {
		limit                                                int64
		name                                                 string
		checkers, budget, threads, files, slots, hashThreads int
		rate                                                 int64
	}{
		{128, "one-connection", 8, 1, 4, 8, 1, 1, 128},
		{128, "four-connections", 8, 4, 4, 8, 4, 1, 32},
		{128, "uneven-budget", 3, 8, 4, 8, 3, 2, 42},
		{128, "configured-threads", 2, 16, 4, 8, 2, 4, 64},
		{128, "default-budget", 32, 0, 4, 20, 16, 1, 8},
		{128, "single-candidate", 8, 8, 4, 1, 1, 4, 128},
		{128, "default-options", 0, 0, 0, 4, 1, 4, 128},
		{128, "cancel-admitted", 8, 2, 4, 8, 2, 1, 64},
		{1, "low-one", 4, 8, 4, 4, 1, 4, 1},
		{2, "low-two", 4, 8, 4, 4, 2, 4, 1},
		{3, "low-three", 4, 8, 4, 4, 3, 2, 1},
		{0, "unlimited", 4, 8, 4, 4, 4, 2, 0},
		{math.MaxInt64, "large-bandwidth", 4, 8, 4, 4, 4, 2, math.MaxInt64 / 4},
		{1, "cancel-low", 4, 8, 4, 4, 1, 4, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, d := hashBudgetFixture(t, tc.files)
			previous := runTransferWorker
			defer func() { runTransferWorker = previous }()
			started, release := make(chan struct{}, tc.files), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			defer unblock()
			var mu sync.Mutex
			active, peak, connections, peakConnections, calls := 0, 0, 0, 0, 0
			var allocated, peakAllocated int64
			runTransferWorker = func(ctx context.Context, kind string, _ uint64, _, to string, rate int64) error {
				settings, _ := ctx.Value(transferSettingsKey{}).(workerSettings)
				if kind != "download" || settings.Threads != tc.hashThreads || rate != tc.rate {
					t.Errorf("worker kind=%s threads=%d rate=%d, want threads=%d rate=%d", kind, settings.Threads, rate, tc.hashThreads, tc.rate)
				}
				mu.Lock()
				active++
				calls++
				connections += max(1, settings.Threads)
				peak = max(peak, active)
				peakConnections = max(peakConnections, connections)
				allocated += rate
				peakAllocated = max(peakAllocated, allocated)
				mu.Unlock()
				defer func() { mu.Lock(); active--; connections -= max(1, settings.Threads); allocated -= rate; mu.Unlock() }()
				started <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				return os.WriteFile(to, []byte("data"), 0600)
			}
			o := Options{Checkers: tc.checkers, Transfers: 1, MaxConnections: tc.budget, DownloadThreads: tc.threads, BWLimit: tc.limit, MaxDelete: -1, Checksum: true}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				plan, err := BuildPlan(ctx, s, d, "copy", o)
				if err == nil {
					for _, a := range plan.Actions {
						if a.Kind != "skip" {
							err = fmt.Errorf("comparison did not match: %+v", a)
							break
						}
					}
				}
				done <- err
			}()
			joined := false
			defer func() {
				unblock()
				cancel()
				if !joined {
					<-done
				}
			}()
			for i := 0; i < tc.slots; i++ {
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("comparison slots did not start")
				}
			}
			if tc.files > tc.slots {
				select {
				case <-started:
					t.Error("comparison exceeded admitted slot count")
				case <-time.After(25 * time.Millisecond):
				}
			}
			canceled := tc.name == "cancel-admitted" || tc.name == "cancel-low"
			if canceled {
				cancel()
			} else {
				unblock()
			}
			runErr := <-done
			if canceled {
				if !errors.Is(runErr, context.Canceled) {
					t.Errorf("cancellation lost: %v", runErr)
				}
			} else if runErr != nil {
				t.Error(runErr)
			}
			joined = true
			budget := tc.budget
			if budget < 1 {
				budget = 16
			}
			wantCalls := tc.files
			if canceled {
				wantCalls = tc.slots
			}
			if active != 0 || calls != wantCalls || peak != tc.slots || peakConnections > budget {
				t.Errorf("active=%d calls=%d peak=%d connections=%d, want calls=%d peak=%d budget=%d", active, calls, peak, peakConnections, wantCalls, tc.slots, budget)
			}
			if allocated != 0 || tc.limit > 0 && peakAllocated > tc.limit {
				t.Errorf("hash bandwidth allocation current=%d peak=%d exceeds %d", allocated, peakAllocated, tc.limit)
			}
			for i := 0; i < tc.files; i++ {
				if got, err := os.ReadFile(d.At(fmt.Sprintf("%02d", i))); err != nil || string(got) != "data" {
					t.Errorf("comparison changed target: %q %v", got, err)
				}
			}
		})
	}
}

func TestSerialHashUsesAvailableBudget(t *testing.T) {
	for _, mode := range []string{"check", "file-plan", "verify-move"} {
		for _, budget := range []int{1, 8} {
			t.Run(fmt.Sprintf("%s/%d", mode, budget), func(t *testing.T) {
				s, d := hashBudgetFixture(t, 1)
				o := Options{Checkers: 4, Transfers: 1, MaxConnections: budget, DownloadThreads: 4, BWLimit: 128, Checksum: true}
				wantThreads, wantRate := min(4, budget), int64(128)
				if mode == "verify-move" && budget == 8 {
					o.Transfers = 2
					wantRate = 64
				}
				previous := runTransferWorker
				defer func() { runTransferWorker = previous }()
				calls := 0
				runTransferWorker = func(ctx context.Context, kind string, _ uint64, _, to string, rate int64) error {
					calls++
					settings, _ := ctx.Value(transferSettingsKey{}).(workerSettings)
					if kind != "download" || settings.Threads != wantThreads || rate != wantRate {
						t.Errorf("worker kind=%s threads=%d rate=%d, want threads=%d rate=%d", kind, settings.Threads, rate, wantThreads, wantRate)
					}
					return os.WriteFile(to, []byte("data"), 0600)
				}
				var err error
				if mode == "check" {
					err = Check(context.Background(), s, d, o)
				} else {
					s.Root, d.Root = s.At("00"), filepath.Join(d.Root, "00")
					if mode == "file-plan" {
						_, err = FilePlan(context.Background(), s, d, false, o)
					} else {
						entry, statErr := s.FS.Stat(context.Background(), s.Root)
						if statErr != nil {
							t.Fatal(statErr)
						}
						err = verifyAndRemove(context.Background(), s, d, Action{Source: &entry}, o)
					}
				}
				if err != nil || calls != 1 {
					t.Errorf("hash execution calls=%d err=%v", calls, err)
				}
				if mode == "verify-move" {
					if got, err := os.ReadFile(d.Root); err != nil || string(got) != "data" {
						t.Errorf("verified move target=%q err=%v", got, err)
					}
					if _, err := s.FS.Stat(context.Background(), s.Root); !errors.Is(err, os.ErrNotExist) {
						t.Errorf("verified move did not remove source: %v", err)
					}
				}
			})
		}
	}
}

func TestLowBandwidthMetadataOnlyPlansDoNotStartTransfers(t *testing.T) {
	for _, mode := range []string{"size-only", "ignore-existing"} {
		t.Run(mode, func(t *testing.T) {
			s, d := hashBudgetFixture(t, 4)
			previous := runTransferWorker
			defer func() { runTransferWorker = previous }()
			runTransferWorker = func(context.Context, string, uint64, string, string, int64) error {
				t.Error("metadata-only plan started content transfer")
				return errors.New("unexpected content transfer")
			}
			o := Options{Transfers: 4, Checkers: 4, MaxConnections: 8, DownloadThreads: 4, BWLimit: 1, MaxDelete: -1, SizeOnly: mode == "size-only", IgnoreExisting: mode == "ignore-existing"}
			plan, err := BuildPlan(context.Background(), s, d, "copy", o)
			if err != nil {
				t.Fatal(err)
			}
			result, err := Execute(context.Background(), plan, o)
			if err != nil || result != (Result{Skipped: 4}) {
				t.Errorf("metadata-only result=%+v err=%v", result, err)
			}
		})
	}
}
