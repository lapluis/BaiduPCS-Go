package uploader

import (
	"bytes"
	"encoding/binary"
	"io"
	"math/big"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func splitSequenceStep(command byte, offset int64, readSize byte) []byte {
	step := make([]byte, 10)
	step[0], step[9] = command, readSize
	binary.LittleEndian.PutUint64(step[1:9], uint64(offset))
	return step
}

// The oracle reads an independent slice and uses arbitrary-precision positions;
// it neither shares the split reader's buffer nor repeats its seek arithmetic.
func FuzzBufferedSplitReadSeekSequence(f *testing.F) {
	for _, offset := range []int64{-1 << 63, -100, -1, 0, 1, 7, 100, 1<<63 - 1} {
		for command := byte(1); command <= 4; command++ {
			sequence := splitSequenceStep(0, 0, 0)
			sequence = append(sequence, splitSequenceStep(command, offset, 2)...)
			sequence = append(sequence, splitSequenceStep(1, 0, 31)...)
			sequence = append(sequence, splitSequenceStep(3, -2, 5)...)
			f.Add([]byte("prefix:abcdefgh:suffix"), uint16(7), uint16(8), sequence)
		}
	}
	f.Add([]byte{}, uint16(0), uint16(0), splitSequenceStep(2, 0, 0))
	f.Add([]byte("\x00\xff\r\n"), uint16(0), uint16(4), splitSequenceStep(0, 0, 31))
	f.Fuzz(func(t *testing.T, data []byte, start, span uint16, sequence []byte) {
		if len(data) > 4096 || len(sequence) > 640 {
			t.Skip()
		}
		begin := int(start) % (len(data) + 1)
		length := int(span) % (len(data) - begin + 1)
		section := data[begin : begin+length]
		reference := bytes.NewReader(section)
		reader := NewBufioSplitUnit(bytes.NewReader(data), transfer.Range{Begin: int64(begin), End: int64(begin + length)}, nil, nil)
		var position int64
		for i := 0; i+10 <= len(sequence); i += 10 {
			step := sequence[i : i+10]
			command := step[0] % 5
			if command != 0 {
				offset := int64(binary.LittleEndian.Uint64(step[1:9]))
				whence := int(command) - 1
				base := int64(0)
				if whence == io.SeekCurrent {
					base = position
				} else if whence == io.SeekEnd {
					base = int64(length)
				}
				target := new(big.Int).Add(big.NewInt(base), big.NewInt(offset))
				valid := whence <= io.SeekEnd && target.Sign() >= 0 && target.IsInt64()
				got, err := reader.Seek(offset, whence)
				if valid {
					position = target.Int64()
					if err != nil || got != position {
						t.Fatalf("step %d: seek(%d,%d)=%d,%v want=%d", i/10, offset, whence, got, err, position)
					}
					if _, err := reference.Seek(position, io.SeekStart); err != nil {
						t.Fatal(err)
					}
				} else if err == nil {
					t.Fatalf("step %d: invalid seek(%d,%d) accepted", i/10, offset, whence)
				}
			}
			// Read after failed seeks too: a rejected operation must preserve
			// both the logical position and any unread prefetched bytes.
			got, want := make([]byte, 1+int(step[9])%64), make([]byte, 1+int(step[9])%64)
			n, err := io.ReadFull(reader, got)
			wn, werr := io.ReadFull(reference, want)
			if n != wn || err != werr || !bytes.Equal(got[:n], want[:wn]) {
				t.Fatalf("step %d: read=%x,%v want=%x,%v", i/10, got[:n], err, want[:wn], werr)
			}
			position += int64(wn)
			extent := reader.Range()
			if reader.Len() != int64(length) || extent.Begin != int64(begin) || extent.End != int64(begin+length) {
				t.Fatal("read or seek changed the part's extent")
			}
		}
	})
}
