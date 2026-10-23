package waitgroup

import "testing"

func TestWaitGroupCanWaitAgainAndBeReused(t *testing.T) {
	defer func() {
		if recover() != nil {
			t.Fatal("waiting or reusing completed group panicked")
		}
	}()
	group := NewWaitGroup(1)
	for round := 0; round < 3; round++ {
		group.AddDelta()
		go group.Done()
		group.Wait()
		if group.Parallel() != 0 {
			t.Fatal("wait returned before parallel slot released")
		}
		group.Wait()
	}
}
