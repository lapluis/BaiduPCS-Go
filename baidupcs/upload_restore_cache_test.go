package baidupcs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func invokeCachedMutation(pcs *BaiduPCS, ctx context.Context, kind string) error {
	switch kind {
	case "restore":
		_, err := pcs.RecycleRestoreWithContext(ctx, 7)
		return err
	case "merge":
		return pcs.UploadCreateSuperFileWithContext(ctx, "session", OverWritePolicy, 8, "/parent/target", map[int]string{0: EmptyContentMD5})
	case "rapid":
		err, _ := pcs.RapidUploadWithContext(ctx, "/parent/target", OverWritePolicy, "session", EmptyContentMD5, EmptyContentMD5, "", "0", 0, 8, 8, 1, []string{EmptyContentMD5})
		return err
	case "fake":
		err, _ := pcs.FakeRapidUploadWithContext(ctx, "/parent/target", OverWritePolicy, 8)
		return err
	default:
		return pcs.RapidUploadNoCheckDir("/parent/target", EmptyContentMD5, EmptyContentMD5, "0", 8)
	}
}

type mutationCacheFixture struct {
	changed          atomic.Bool
	lists, accounts  atomic.Int32
	blockListing     bool
	started, release chan struct{}
	blockOnce        sync.Once
	mode             string
	cancel           context.CancelFunc
}

func newMutationCacheFixture(t *testing.T, mode string) (*BaiduPCS, *mutationCacheFixture) {
	t.Helper()
	f := &mutationCacheFixture{mode: mode, started: make(chan struct{}), release: make(chan struct{})}
	pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		switch {
		case r.URL.Path == "/api/list":
			f.lists.Add(1)
			changed := f.changed.Load()
			if f.blockListing {
				f.blockOnce.Do(func() { close(f.started); <-f.release })
			}
			if changed {
				return response(r, fmt.Sprintf(`{"errno":0,"list":[{"fs_id":7,"path":%q,"size":1}]}`, path.Join(r.URL.Query().Get("dir"), "fresh"))), nil
			}
			return response(r, `{"errno":0,"list":[]}`), nil
		case r.URL.Path == "/api/user/getinfo":
			f.accounts.Add(1)
			return response(r, `{"errno":0,"records":[{"uk":7}]}`), nil
		case r.URL.Query().Get("method") == "meta":
			return response(r, `{"error_code":31066}`), nil
		}
		f.changed.Store(true) // The server has committed, even if its reply fails.
		switch f.mode {
		case "lost":
			return nil, errors.New("fixture reply lost after commit")
		case "remote":
			return response(r, `{"error_code":31066,"errno":31066}`), nil
		case "invalid-json":
			return response(r, `{`), nil
		case "canceled":
			f.cancel()
			return nil, r.Context().Err()
		}
		return response(r, `{"error_code":0,"errno":0,"return_type":1,"uploadid":"session","extra":{"list":[{"fs_id":7}]}}`), nil
	})
	pcs.SetPanUserAgent("fixture")
	pcs.lazyInit()
	return pcs, f
}

func TestUploadAndRestoreInvalidatePotentiallyChangedListings(t *testing.T) {
	for _, kind := range []string{"restore", "merge", "rapid", "fake", "unchecked"} {
		for _, mode := range []string{"success", "lost", "remote", "invalid-json", "canceled"} {
			if kind == "unchecked" && mode == "canceled" {
				continue
			} // Legacy-only API.
			t.Run(kind+"/"+mode, func(t *testing.T) {
				pcs, f := newMutationCacheFixture(t, mode)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				f.cancel = cancel
				dirs := []string{"/", "/parent", "/parent/target", "/parent/target/sub", "/parent/target-other", "/other", "relative"}
				orders := []*OrderOptions{DefaultOrderOptions, {By: OrderByTime, Order: OrderDesc}}
				for _, dir := range dirs {
					for _, order := range orders {
						if _, err := pcs.CacheFilesDirectoriesList(dir, order); err != nil {
							t.Fatal(err)
						}
					}
				}
				if uk, err := pcs.CacheUK(); err != nil || uk != 7 {
					t.Fatal("account cache setup", uk, err)
				}
				err := invokeCachedMutation(pcs, ctx, kind)
				if (err != nil) != (mode != "success") || !f.changed.Load() {
					t.Fatal("mutation fixture did not commit expected result", err, f.changed.Load())
				}
				clone := pcs.CopyPCS()
				wantCalls := len(dirs) * len(orders)
				for _, dir := range dirs {
					changed := kind == "restore" || dir == "/parent" || kind == "unchecked" && (dir == "/parent/target" || strings.HasPrefix(dir, "/parent/target/"))
					wantCount := 0
					if changed {
						wantCount = 1
						wantCalls += len(orders)
					}
					for _, order := range orders {
						files, err := clone.CacheFilesDirectoriesList(dir, order)
						if err != nil || len(files) != wantCount {
							t.Errorf("stale listing %q: count=%d want=%d err=%v", dir, len(files), wantCount, err)
						}
					}
				}
				if int(f.lists.Load()) != wantCalls {
					t.Errorf("listing requests=%d want=%d", f.lists.Load(), wantCalls)
				}
				if uk, err := clone.CacheUK(); err != nil || uk != 7 || f.accounts.Load() != 1 {
					t.Fatal("directory invalidation evicted unrelated account cache", uk, err, f.accounts.Load())
				}
			})
		}
	}
}

func TestUploadAndRestoreRejectInflightStaleCachePublication(t *testing.T) {
	for _, kind := range []string{"restore", "merge", "rapid", "fake", "unchecked"} {
		for _, mode := range []string{"success", "lost"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				pcs, f := newMutationCacheFixture(t, mode)
				f.blockListing = true
				unblock := sync.OnceFunc(func() { close(f.release) })
				done := make(chan error, 1)
				go func() { _, err := pcs.CacheFilesDirectoriesList("/parent", nil); done <- err }()
				joined := false
				defer func() {
					unblock()
					if !joined {
						<-done
					}
				}()
				select {
				case <-f.started:
				case <-time.After(time.Second):
					t.Fatal("listing did not start")
				}
				err := invokeCachedMutation(pcs, context.Background(), kind)
				if (err != nil) != (mode == "lost") {
					t.Fatal("unexpected mutation result", err)
				}
				unblock()
				listingErr := <-done
				joined = true
				if listingErr != nil {
					t.Fatal(listingErr)
				}
				for i := 0; i < 2; i++ {
					files, err := pcs.CacheFilesDirectoriesList("/parent", nil)
					if err != nil || len(files) != 1 {
						t.Error("old response re-entered cache", len(files), err)
					}
				}
				if f.lists.Load() != 2 {
					t.Fatal("fresh listing was not fetched once and cached", f.lists.Load())
				}
			})
		}
	}
}
