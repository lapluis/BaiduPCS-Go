package rio

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestFileReaderLengthStartsAtCurrentOffset(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("abcdef"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(3, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	reader := NewFileReaderLen64(f)
	if reader.Len() != 3 {
		t.Fatalf("remaining length %d, want 3", reader.Len())
	}
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "def" || reader.Len() != 0 {
		t.Fatal("remaining read or length incorrect", err)
	}
	if err := f.Truncate(1); err != nil {
		t.Fatal(err)
	}
	if reader.Len() != 0 {
		t.Fatal("truncation produced negative remaining length")
	}
	at := NewFileReaderAtLen64(f)
	if at.Len() != 1 {
		t.Fatal("random access reader should expose full file length")
	}
}
