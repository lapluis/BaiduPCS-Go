package transfer

import "testing"

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
