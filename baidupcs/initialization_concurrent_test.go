package baidupcs

import (
	"runtime"
	"sync"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/expires/cachemap"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/internal/panhome"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestConcurrentPCSInitializationPublishesOneRuntime(t *testing.T) {
	previous := runtime.GOMAXPROCS(8)
	defer runtime.GOMAXPROCS(previous)
	for _, provided := range []bool{false, true} {
		t.Run(map[bool]string{false: "zero", true: "provided-client"}[provided], func(t *testing.T) {
			for round := 0; round < 256; round++ {
				pcs := &BaiduPCS{}
				var supplied *requester.HTTPClient
				if provided {
					supplied = requester.NewHTTPClient()
					pcs = NewPCSWithClient(1, supplied)
				}
				type snapshot struct {
					client *requester.HTTPClient
					cache  *cachemap.CacheOpMap
					home   *panhome.PanHome
					ua     string
				}
				snapshots := make([]snapshot, 32)
				start := make(chan struct{})
				var joined sync.WaitGroup
				for index := range snapshots {
					joined.Add(1)
					go func(index int) {
						defer joined.Done()
						<-start
						client := pcs.GetClient()
						snapshots[index] = snapshot{client, pcs.cacheOpMap, pcs.ph, pcs.panUA}
						_ = pcs.GetBDUSS()
						_ = pcs.GetBAIDUID()
					}(index)
				}
				close(start)
				joined.Wait()
				want := snapshot{pcs.client, pcs.cacheOpMap, pcs.ph, NetdiskUA}
				if want.client == nil || want.cache == nil || want.home == nil || supplied != nil && want.client != supplied {
					t.Fatal("initialization lost required or supplied objects")
				}
				for _, got := range snapshots {
					if got != want {
						t.Fatalf("round %d observed different initialization snapshots: client=%t cache=%t home=%t ua=%t", round, got.client != want.client, got.cache != want.cache, got.home != want.home, got.ua != want.ua)
					}
				}
			}
		})
	}
}

func TestPCSInitializationKeepsUserAgentAndCopyState(t *testing.T) {
	for _, before := range []bool{false, true} {
		pcs := NewPCS(1, "")
		if before {
			pcs.SetPanUserAgent("fixture custom user agent")
		}
		client := pcs.GetClient()
		if !before {
			pcs.SetPanUserAgent("fixture custom user agent")
		}
		copy := pcs.CopyPCS()
		if copy.GetClient() != client || copy.cacheOpMap != pcs.cacheOpMap || copy.ph != pcs.ph || pcs.getPanUAHeader()["User-Agent"] != "fixture custom user agent" || copy.getPanUAHeader()["User-Agent"] != "fixture custom user agent" {
			t.Fatal("initialization changed configured user agent or copied runtime")
		}
	}
}

func TestPCSFirstCookieReadKeepsConfiguredJar(t *testing.T) {
	pcs := NewPCSWithCookieStr(1, "BDUSS=fixture-cookie; BAIDUID=fixture-id")
	if pcs.GetBDUSS() != "fixture-cookie" || pcs.GetBAIDUID() != "fixture-id" {
		t.Fatal("first initialization lost explicitly configured cookies")
	}
	copy := pcs.CopyPCS()
	if copy.GetBDUSS() != "fixture-cookie" || copy.GetBAIDUID() != "fixture-id" {
		t.Fatal("copied runtime lost configured cookies")
	}
	zero := &BaiduPCS{}
	if zero.GetBDUSS() != "" || zero.GetBAIDUID() != "" || zero.GetClient() == nil {
		t.Fatal("zero-value cookie reads did not initialize an empty client")
	}
}
