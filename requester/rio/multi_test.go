package rio

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

type textReader64 struct{ *strings.Reader }

func (r textReader64) Len() int64 { return int64(r.Reader.Len()) }

func TestMultiReadersSkipNilAndOwnInputList(t *testing.T) {
	defer func() {
		if recover() != nil {
			t.Fatal("nil component caused panic")
		}
	}()
	inputs := []ReaderLen{nil, strings.NewReader("abc"), nil}
	reader := MultiReaderLen(inputs...)
	inputs[1] = strings.NewReader("incorrect")
	if reader.Len() != 3 {
		t.Fatal("length disagrees with constructed reader")
	}
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "abc" || reader.Len() != 0 {
		t.Fatal("combined reader data or remaining length incorrect")
	}
	inputs64 := []ReaderLen64{nil, textReader64{strings.NewReader("abc")}, nil}
	reader64 := MultiReaderLen64(inputs64...)
	inputs64[1] = textReader64{strings.NewReader("incorrect")}
	if reader64.Len() != 3 {
		t.Fatal("64-bit length disagrees with constructed reader")
	}
	data, err = io.ReadAll(reader64)
	if err != nil || string(data) != "abc" || reader64.Len() != 0 {
		t.Fatal("64-bit combined reader incorrect")
	}
}

func TestMultiReaderLen(t *testing.T) {
	rd1, rd2 := strings.NewReader("asdkfljalf"), strings.NewReader("---asva sdf")
	multi := MultiReaderLen(rd1, rd2)
	fmt.Println(multi.Len())
	io.Copy(os.Stdout, multi)
}
