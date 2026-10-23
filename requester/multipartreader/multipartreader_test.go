package multipartreader

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
)

type fileReader struct{ *strings.Reader }

func (r fileReader) Len() int64 { return int64(r.Reader.Len()) }

func TestMultipartPartsRemainSeparate(t *testing.T) {
	mr := NewMultipartReader()
	mr.AddFormField("first", strings.NewReader("alpha"))
	mr.AddFormField("second", strings.NewReader("beta"))
	mr.AddFormFile("file", "sample.txt", fileReader{strings.NewReader("payload")})
	if err := mr.CloseMultipart(); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(mr)
	if err != nil || int64(len(body)) != mr.Len() {
		t.Fatal("content length differs from actual body", err)
	}
	_, params, err := mime.ParseMediaType(mr.ContentType())
	if err != nil {
		t.Fatal(err)
	}
	parsed := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for _, want := range []struct{ name, data string }{{"first", "alpha"}, {"second", "beta"}, {"file", "payload"}} {
		part, err := parsed.NextPart()
		if err != nil {
			t.Fatal("missing part:", err)
		}
		data, err := io.ReadAll(part)
		if err != nil || part.FormName() != want.name || string(data) != want.data {
			t.Fatalf("part %s contains wrong data: %q (%v)", want.name, data, err)
		}
	}
	if _, err := parsed.NextPart(); err != io.EOF {
		t.Fatal("unexpected extra part", err)
	}
}
