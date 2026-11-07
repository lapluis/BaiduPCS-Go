package baidupcs

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestListingKeepsInitialOrderAcrossPages(t *testing.T) {
	for _, cached := range []bool{false, true} {
		for _, legacy := range []bool{false, true} {
			for _, defaults := range []bool{false, true} {
				t.Run(fmt.Sprintf("cached=%t/legacy=%t/defaults=%t", cached, legacy, defaults), func(t *testing.T) {
					initial := OrderOptions{By: OrderByName, Order: OrderAsc}
					options := &OrderOptions{By: initial.By, Order: initial.Order}
					if defaults {
						previous := DefaultOrderOptions
						DefaultOrderOptions = options
						defer func() { DefaultOrderOptions = previous }()
					}
					calls := 0
					pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
						calls++
						q := req.URL.Query()
						if calls == 1 {
							// This is synchronous with request processing, so it
							// changes the input between pages without a data race.
							options.By, options.Order = OrderByTime, OrderDesc
							return response(req, pageBody(1, maxListNum)), nil
						}
						if q.Get("order") != "name" || q.Get("desc") != "0" {
							return response(req, pageBody(1, 1)), nil
						}
						return response(req, pageBody(maxListNum+1, 1)), nil
					})
					queryOptions := options
					if defaults {
						queryOptions = nil
					}
					var got FileDirectoryList
					var err error
					switch {
					case cached && legacy:
						got, err = pcs.CacheFilesDirectoriesList("/Temp", queryOptions)
					case cached:
						got, err = pcs.CacheFilesDirectoriesListWithContext(context.Background(), "/Temp", queryOptions)
					case legacy:
						got, err = pcs.FilesDirectoriesList("/Temp", queryOptions)
					default:
						got, err = pcs.FilesDirectoriesListWithContext(context.Background(), "/Temp", queryOptions)
					}
					if err != nil || len(got) != maxListNum+1 || calls != 2 {
						t.Fatalf("order changed between pages: entries=%d calls=%d err=%v", len(got), calls, err)
					}
					if options.By != OrderByTime || options.Order != OrderDesc {
						t.Fatal("query overwrote caller's updated options")
					}
					if cached {
						got, err = pcs.CacheFilesDirectoriesList("/Temp", &initial)
						if err != nil || len(got) != maxListNum+1 || calls != 2 {
							t.Fatalf("initial-order cache was not reused: entries=%d calls=%d err=%v", len(got), calls, err)
						}
					}
				})
			}
		}
	}
}

type listingWaitContext struct {
	context.Context
	reached chan struct{}
	once    sync.Once
}

func (c *listingWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.reached) })
	return c.Context.Done()
}

func TestCachedListingKeepsOrderWhileWaitingForKey(t *testing.T) {
	options := &OrderOptions{By: OrderByName, Order: OrderAsc}
	calls := 0
	pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Query().Get("order") == "name" && req.URL.Query().Get("desc") == "0" {
			return response(req, pageBody(1, 1)), nil
		}
		return response(req, pageBody(2, 1)), nil
	})
	pcs.lazyInit()
	key := directoryCacheKey{"/Temp", OrderByName, OrderAsc}
	pool := pcs.cacheOpMap.LazyInitCachePoolOp(OperationFilesDirectoriesList)
	pool.LockKey(key)
	unlock := sync.OnceFunc(func() { pool.UnlockKey(key) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wait := &listingWaitContext{Context: ctx, reached: make(chan struct{})}
	done := make(chan struct{})
	var got FileDirectoryList
	var err error
	go func() {
		defer close(done)
		got, err = pcs.CacheFilesDirectoriesListWithContext(wait, "/Temp", options)
	}()
	defer func() { cancel(); unlock(); <-done }()
	select {
	case <-wait.reached:
	case <-ctx.Done():
		t.Fatal("listing did not reach cache wait")
	}
	options.By, options.Order = OrderByTime, OrderDesc
	unlock()
	<-done
	if err != nil || len(got) != 1 || got[0].FsID != 1 {
		t.Fatalf("cache wait changed query order: entries=%v err=%v", got, err)
	}
	got, err = pcs.CacheFilesDirectoriesList("/Temp", &OrderOptions{By: OrderByName, Order: OrderAsc})
	if err != nil || len(got) != 1 || got[0].FsID != 1 || calls != 1 {
		t.Fatalf("wrong-order data published to initial key: entries=%v calls=%d err=%v", got, calls, err)
	}
	got, err = pcs.CacheFilesDirectoriesList("/Temp", options)
	if err != nil || len(got) != 1 || got[0].FsID != 2 || calls != 2 {
		t.Fatalf("new query did not use updated options: entries=%v calls=%d err=%v", got, calls, err)
	}
}
