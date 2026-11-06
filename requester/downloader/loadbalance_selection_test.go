package downloader

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestEmptyLoadBalancerSelection(t *testing.T) {
	list := NewLoadBalancerResponseList(nil)
	if list.SequentialGet() != nil || list.RandomGet() != nil {
		t.Fatal("empty selection must return nil")
	}
}

func TestRandomNumberBounds(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	for _, bounds := range [][2]int{{0, 0}, {7, 7}, {-3, -3}, {5, 0}, {-5, 0}, {minInt, maxInt}, {0, maxInt}, {minInt, 0}} {
		t.Run(fmt.Sprint(bounds), func(t *testing.T) {
			low, high := bounds[0], bounds[1]
			if low > high {
				low, high = high, low
			}
			for i := 0; i < 1000; i++ {
				v := RandomNumber(bounds[0], bounds[1])
				if low == high && v != low || low != high && (v < low || v >= high) {
					t.Fatalf("out of bounds: %d in [%d,%d)", v, low, high)
				}
			}
		})
	}
}

func TestConcurrentLoadBalancerSelection(t *testing.T) {
	servers := []*LoadBalancerResponse{{URL: "a"}, {URL: "b"}, {URL: "c"}}
	list := NewLoadBalancerResponseList(servers)
	var counts [3]atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if err := recover(); err != nil {
					t.Errorf("selection panicked: %v", err)
				}
			}()
			for j := 0; j < 1000; j++ {
				selected := list.SequentialGet()
				found := false
				for k, server := range servers {
					if selected == server {
						counts[k].Add(1)
						found = true
						break
					}
				}
				if !found {
					t.Error("selection returned an unknown server")
					return
				}
				if list.RandomGet() == nil {
					t.Error("random selection lost all servers")
					return
				}
			}
		}()
	}
	wg.Wait()
	for i := range counts {
		if got := counts[i].Load(); got != 4000 {
			t.Errorf("server %d selected %d times, want 4000", i, got)
		}
	}
}
