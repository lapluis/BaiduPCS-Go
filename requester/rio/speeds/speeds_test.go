package speeds

import (
	"sync"
	"testing"
	"time"
)

func TestSpeedsSamplingAndReset(t *testing.T) {
	var s Speeds
	s.Add(1000)
	s.mu.Lock()
	s.nowTime = time.Now().Add(-time.Second)
	s.mu.Unlock()
	rate := s.GetSpeeds()
	if rate < 900 || rate > 1000 {
		t.Fatalf("incorrect sample: %d", rate)
	}
	s.mu.Lock()
	count := s.count
	s.mu.Unlock()
	if count != 0 {
		t.Fatal("sample did not reset")
	}
	s.Add(25)
	s.mu.Lock()
	count = s.count
	s.mu.Unlock()
	if count != 25 {
		t.Fatal("new sample lost bytes")
	}
}
func TestConcurrentSpeedAccounting(t *testing.T) {
	var s Speeds
	s.SetInterval(time.Hour)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				s.Add(1)
				s.GetSpeeds()
				s.SetInterval(time.Hour)
			}
		}()
	}
	wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count != 16000 {
		t.Fatalf("concurrent accounting lost bytes: %d", s.count)
	}
}
