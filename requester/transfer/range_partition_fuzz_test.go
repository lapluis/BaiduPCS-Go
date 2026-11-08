package transfer

import (
	"math"
	"testing"
)

func FuzzRangePartitions(f *testing.F) {
	for _, total := range []uint64{0, 1, 2, uint64(DefaultBlockSize - 1), uint64(DefaultBlockSize), uint64(DefaultBlockSize + 1), 1 << 32, 1 << 53, math.MaxInt64} {
		for _, begin := range []uint64{0, total / 2, total} {
			f.Add(total, begin, uint64(0), uint8(0))
			f.Add(total, begin, uint64(math.MaxInt64), uint8(63))
		}
	}
	f.Fuzz(func(t *testing.T, rawTotal, rawBegin, rawBlock uint64, rawParallel uint8) {
		total := int64(rawTotal & math.MaxInt64)
		begin := int64(rawBegin % (uint64(total) + 1))
		remaining := total - begin
		block := int64(rawBlock & math.MaxInt64)
		// Bound enumeration without allocating file data or excluding large offsets.
		minimum := remaining / 128
		if remaining%128 != 0 {
			minimum++
		}
		if block == 0 {
			if minimum > DefaultBlockSize {
				block = minimum
			}
		} else if block < minimum {
			block = minimum
		}
		for _, gen := range []*RangeListGen{
			NewRangeListGenDefault(total, begin, 0, int(rawParallel%64)),
			NewRangeListGenBlockSize(total, begin, block),
		} {
			cursor := begin
			generated := 0
			for {
				// Observing the generator must neither consume nor change a range.
				before := gen.LoadBegin()
				count := gen.RangeCount()
				gen.LoadBlockSize()
				if gen.LoadBegin() != before || count < 0 || gen.IsDone() != (cursor == total) {
					t.Fatal("observation changed progress or reported an invalid state")
				}
				index, r := gen.GenRange()
				if r == nil {
					if cursor != total || count != 0 || index != generated {
						t.Fatalf("premature end: cursor=%d total=%d count=%d index=%d generated=%d", cursor, total, count, index, generated)
					}
					break
				}
				if index != generated || r.Begin != cursor || r.End <= r.Begin || r.End > total || r.Len() != r.End-cursor || gen.LoadBegin() != r.End {
					t.Fatalf("invalid partition: index=%d range=%v cursor=%d total=%d", index, r, cursor, total)
				}
				if gen.RangeGenMode() == RangeGenMode_BlockSize && gen.RangeCount() != count-1 {
					t.Fatal("fixed-block remaining count did not decrease by one")
				}
				cursor = r.End
				generated++
				if generated > 128 {
					t.Fatal("partition generation failed to terminate within the bounded fixture")
				}
			}
			if _, r := gen.GenRange(); r != nil {
				t.Fatal("exhausted generator produced another range")
			}
		}
	})
}
