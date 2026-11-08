package multipartreader

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
	"unicode/utf8"
)

// Check the upload wire format against the standard library's MIME parser,
// including binary payloads and the distinction between empty and short files.
func FuzzMultipartRoundTrip(f *testing.F) {
	for _, name := range []string{"", "file", "中文文件", `quote"slash\`, "line\r\nbreak", "; filename=other", "%0D%0A", " trailing "} {
		f.Add(name, []byte("\x00payload\r\n--boundary\xff"), uint8(0))
		f.Add(name, []byte{}, uint8(1))
		f.Add(name, []byte("short"), uint8(2))
	}
	f.Fuzz(func(t *testing.T, name string, payload []byte, mode uint8) {
		if len(name) > 512 || len(payload) > 32<<10 || !utf8.ValidString(name) {
			t.Skip()
		}
		// These names are legal header parameters after CR/LF encoding. Other
		// ASCII control characters are outside this round-trip property's domain.
		for _, r := range name {
			if (r < 32 && r != '\r' && r != '\n' && r != '\t') || r == 127 {
				t.Skip()
			}
		}
		declared := int64(len(payload))
		switch mode % 3 {
		case 1:
			if declared > 0 {
				declared--
			}
		case 2:
			declared++
		}
		body := NewMultipartReader()
		body.AddFormField(name, bytes.NewReader(payload))
		body.AddFormField("tail-field", strings.NewReader("field sentinel"))
		body.AddFormFile(name, name, declaredFile{bytes.NewReader(payload), declared})
		body.AddFormFile("tail-file", "sentinel", fileReader{strings.NewReader("file sentinel")})
		if err := body.CloseMultipart(); err != nil {
			t.Fatal(err)
		}
		wire, err := io.ReadAll(body)
		if declared > int64(len(payload)) {
			if err != io.ErrUnexpectedEOF {
				t.Fatalf("truncated file accepted: %v", err)
			}
			return
		}
		if err != nil || int64(len(wire)) != body.Len() {
			t.Fatalf("body length=%d declared=%d err=%v", len(wire), body.Len(), err)
		}
		mediaType, params, err := mime.ParseMediaType(body.ContentType())
		if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
			t.Fatalf("invalid content type: %q %v", body.ContentType(), err)
		}
		parsed := multipart.NewReader(bytes.NewReader(wire), params["boundary"])
		encodedName := strings.ReplaceAll(strings.ReplaceAll(name, "\r", "%0D"), "\n", "%0A")
		for _, want := range []struct {
			name, filename string
			data           []byte
			file           bool
		}{
			{encodedName, "", payload, false},
			{"tail-field", "", []byte("field sentinel"), false},
			{encodedName, encodedName, payload[:declared], true},
			{"tail-file", "sentinel", []byte("file sentinel"), true},
		} {
			part, err := parsed.NextPart()
			if err != nil {
				t.Fatalf("missing part: %v", err)
			}
			disposition, parameters, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			filename, hasFilename := parameters["filename"]
			if err != nil || disposition != "form-data" || parameters["name"] != want.name || filename != want.filename || hasFilename != want.file || len(part.Header) != 1 {
				t.Fatalf("part headers changed: %v, err=%v", part.Header, err)
			}
			data, err := io.ReadAll(part)
			if err != nil || !bytes.Equal(data, want.data) {
				t.Fatalf("part payload changed: got=%d want=%d err=%v", len(data), len(want.data), err)
			}
		}
		if _, err := parsed.NextPart(); err != io.EOF {
			t.Fatalf("unexpected trailing part: %v", err)
		}
	})
}
