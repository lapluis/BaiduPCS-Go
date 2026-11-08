package expires_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/expires"
)

func TestConcurrentExpirationInvalidationAndReads(t *testing.T) {
	for _, data := range []bool{false, true} {
		name := map[bool]string{false: "expiry", true: "data-expiry"}[data]
		t.Run(name, func(t *testing.T) {
			var entry expires.Expires = expires.NewExpires(time.Hour)
			if data {
				entry = expires.NewDataExpires("fixture payload", time.Hour)
			}
			deadline := entry.GetExpires()
			start := make(chan struct{})
			var group sync.WaitGroup
			group.Add(4)
			go func() {
				defer group.Done()
				<-start
				for i := 0; i < 10000; i++ {
					entry.SetExpires(i%2 == 0)
				}
			}()
			for reader := 0; reader < 3; reader++ {
				go func() {
					defer group.Done()
					<-start
					for i := 0; i < 1000; i++ {
						_ = entry.IsExpires()
						_ = entry.String()
						if !entry.GetExpires().Equal(deadline) {
							t.Error("manual invalidation changed the expiration deadline")
							return
						}
					}
				}()
			}
			close(start)
			group.Wait()
			entry.SetExpires(true)
			if !entry.IsExpires() || !strings.Contains(entry.String(), "abort: true") {
				t.Error("manual expiration was not published")
			}
			entry.SetExpires(false)
			if entry.IsExpires() || !strings.Contains(entry.String(), "abort: false") {
				t.Error("clearing manual expiration changed the future deadline")
			}
			if data && entry.(expires.DataExpires).Data() != "fixture payload" {
				t.Error("expiration updates changed the cached payload")
			}
		})
	}
}

func TestManualExpirationCannotReviveElapsedDeadline(t *testing.T) {
	deadline := time.Now().Add(-time.Hour)
	entry := expires.NewExpiresAt(deadline)
	for _, manual := range []bool{false, true, false} {
		entry.SetExpires(manual)
		if !entry.IsExpires() || !entry.GetExpires().Equal(deadline) {
			t.Fatalf("manual=%t revived an elapsed deadline", manual)
		}
	}
}
