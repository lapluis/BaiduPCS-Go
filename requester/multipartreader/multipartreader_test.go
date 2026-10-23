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

type declaredFile struct {
	io.Reader
	size int64
}

func (r declaredFile) Len() int64 { return r.size }

func TestMultipartRequiresDeclaredPartLength(t *testing.T) {
	mr := NewMultipartReader()
	mr.AddFormFile("file", "a", declaredFile{strings.NewReader("abc"), 5})
	if err := mr.CloseMultipart(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(mr); err != io.ErrUnexpectedEOF {
		t.Fatalf("short source error = %v", err)
	}

	mr = NewMultipartReader()
	mr.AddFormFile("file", "a", declaredFile{strings.NewReader("abcdef"), 3})
	if err := mr.CloseMultipart(); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(mr)
	if err != nil || int64(len(body)) != mr.Len() {
		t.Fatal("extra source bytes exceeded declared content length", err)
	}
	_, params, _ := mime.ParseMediaType(mr.ContentType())
	part, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).NextPart()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(part)
	if err != nil || string(data) != "abc" {
		t.Fatal("source was not bounded to declared length")
	}
}

func TestMultipartClosedBodyCannotBeChanged(t *testing.T) {
	mr := NewMultipartReader()
	mr.AddFormField("first", strings.NewReader("value"))
	if err := mr.CloseMultipart(); err != nil {
		t.Fatal(err)
	}
	length := mr.Len()
	mr.AddFormField("late", strings.NewReader("extra"))
	mr.AddFormFile("late-file", "a", fileReader{strings.NewReader("extra")})
	if mr.Len() != length {
		t.Fatal("closed body's content length changed")
	}
	body, err := io.ReadAll(mr)
	if err != nil || int64(len(body)) != length {
		t.Fatal("closed body length incorrect")
	}
}

func TestMultipartRejectsNegativePartLength(t *testing.T) {
	mr := NewMultipartReader()
	mr.AddFormFile("file", "a", declaredFile{strings.NewReader("abc"), -1})
	if err := mr.CloseMultipart(); err == nil {
		t.Fatal("negative part size accepted")
	}
}

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

func TestMultipartEscapesHeaderParameters(t *testing.T) {
	mr := NewMultipartReader()
	field := `field"with\slash`
	filename := "file\"with\\slash\r\nX-Injected: yes.txt"
	mr.AddFormFile(field, filename, fileReader{strings.NewReader("payload")})
	if err := mr.CloseMultipart(); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(mr)
	if err != nil || int64(len(body)) != mr.Len() {
		t.Fatal("incorrect body length", err)
	}
	_, params, err := mime.ParseMediaType(mr.ContentType())
	if err != nil {
		t.Fatal(err)
	}
	part, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).NextPart()
	if err != nil {
		t.Fatal("invalid multipart header:", err)
	}
	_, disposition, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil {
		t.Fatal(err)
	}
	if disposition["name"] != field || disposition["filename"] != "file\"with\\slash%0D%0AX-Injected: yes.txt" {
		t.Fatal("header parameters were not escaped correctly")
	}
	if part.Header.Get("X-Injected") != "" {
		t.Fatal("filename injected a MIME header")
	}
	data, err := io.ReadAll(part)
	if err != nil || string(data) != "payload" {
		t.Fatal("file data changed")
	}
}
