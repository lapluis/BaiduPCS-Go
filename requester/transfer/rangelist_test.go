package transfer

import (
	"sort"
	"sync"
	"testing"
)

func TestDefaultRangeWithZeroParallel(t *testing.T) {
	defer func() {
		if recover() != nil {
			t.Fatal("zero parallel caused division by zero")
		}
	}()
	gen := NewRangeListGenDefault(10, 0, 0, 0)
	if gen.LoadBlockSize() <= 0 || gen.RangeCount() != 1 {
		t.Fatal("invalid default generator")
	}
	_, r := gen.GenRange()
	if r == nil || r.Begin != 0 || r.End != 10 || !gen.IsDone() || gen.RangeCount() != 0 {
		t.Fatal("default generator failed")
	}
}

func TestConcurrentRangeGeneration(t *testing.T) {
	gen := NewRangeListGenBlockSize(1000, 0, 7)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ranges []*Range
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				gen.RangeCount()
				gen.LoadBlockSize()
				gen.IsDone()
				_, r := gen.GenRange()
				if r == nil {
					return
				}
				mu.Lock()
				ranges = append(ranges, r)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Begin < ranges[j].Begin })
	var end int64
	for _, r := range ranges {
		if r.Begin != end || r.End <= r.Begin {
			t.Fatal("overlapping or missing range")
		}
		end = r.End
	}
	if end != 1000 || gen.RangeCount() != 0 {
		t.Fatal("incomplete concurrent generation")
	}
}

func TestBlockRangeCountMatchesGeneration(t *testing.T) {
	for _, tc := range []struct{ total, begin, block int64 }{{12, 3, 4}, {12, 4, 4}, {12, 12, 4}, {10, 11, 4}, {0, 0, 0}, {10, 0, 0}, {10, 0, -1}} {
		func() {
			defer func() {
				if recover() != nil {
					t.Errorf("range count panicked: %+v", tc)
				}
			}()
			gen := NewRangeListGenBlockSize(tc.total, tc.begin, tc.block)
			count := gen.RangeCount()
			generated := 0
			for {
				_, r := gen.GenRange()
				if r == nil {
					break
				}
				generated++
				if generated > 20 {
					t.Fatal("generation did not finish")
				}
			}
			if count != generated {
				t.Errorf("%+v: count %d, generated %d", tc, count, generated)
			}
		}()
	}
}

func TestBlockRangeEndDoesNotOverflow(t *testing.T) {
	gen := NewRangeListGenBlockSize(1<<63-1, (1<<63-1)-2, 8)
	_, r := gen.GenRange()
	if r == nil || r.End != 1<<63-1 || r.Begin != (1<<63-1)-2 {
		t.Fatalf("invalid large range: %v", r)
	}
	if _, r := gen.GenRange(); r != nil {
		t.Fatal("range after end")
	}
}
