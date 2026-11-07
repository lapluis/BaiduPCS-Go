package downloader

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/expires"
)

func TestResetControllerCountsEveryBurstAttempt(t *testing.T) {
	for _, limit := range []int{1, 2, 64} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			controller := NewResetController(limit)
			for i := 0; i < limit; i++ {
				if !controller.CanReset() {
					t.Fatalf("budget closed before attempt %d of %d", i+1, limit)
				}
				controller.AddResetNum()
			}
			if controller.CanReset() {
				t.Fatal("burst attempts were lost from the budget")
			}
		})
	}
}

func TestResetControllerConcurrentAccountingKeepsLastSlot(t *testing.T) {
	const limit = 64
	controller := NewResetController(limit)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < limit-1; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			controller.AddResetNum()
		}()
	}
	close(start)
	wg.Wait()
	if !controller.CanReset() {
		t.Fatal("concurrent accounting consumed more than 63 slots")
	}
	controller.AddResetNum()
	if controller.CanReset() {
		t.Fatal("concurrent accounting lost one or more attempts")
	}
}

func TestResetControllerExpiresOnlyStaleAttempts(t *testing.T) {
	controller := NewResetController(3)
	// Two distinct attempts can have the same expiration timestamp.
	for i := 0; i < 2; i++ {
		controller.resetEntity[expires.NewExpiresAt(time.Unix(1, 0))] = struct{}{}
	}
	controller.resetEntity[expires.NewExpires(time.Hour)] = struct{}{}
	if !controller.CanReset() {
		t.Fatal("expired attempts did not release their slots")
	}
	controller.AddResetNum()
	if !controller.CanReset() {
		t.Fatal("partial expiry did not leave the second free slot")
	}
	controller.AddResetNum()
	if controller.CanReset() {
		t.Fatal("expiry discarded an unexpired attempt")
	}
}
