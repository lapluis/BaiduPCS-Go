package checksum

import (
	"fmt"
	"io"
)

// ValidateSnapshot checks that the open file still matches the metadata used
// for its checksums, including when handing the file to an uploader.
func (lfc *LocalFileChecksum) ValidateSnapshot() error { return lfc.checkSnapshot() }

// checkSnapshot guards publication of checksums tied to the metadata captured
// by OpenPath. It is also used after reading to detect changes during hashing.
func (lfc *LocalFileChecksum) checkSnapshot() error {
	if lfc.file == nil {
		return ErrFileIsNil
	}
	if lfc.Length < 0 {
		return fmt.Errorf("invalid file size: %d", lfc.Length)
	}
	info, err := lfc.file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < lfc.Length {
		return fmt.Errorf("file shrank from %d to %d bytes: %w", lfc.Length, info.Size(), io.ErrUnexpectedEOF)
	}
	if info.Size() > lfc.Length {
		return fmt.Errorf("file size changed from %d to %d bytes", lfc.Length, info.Size())
	}
	if lfc.ModTimeNS != 0 && lfc.ModTimeNS != info.ModTime().UnixNano() {
		return fmt.Errorf("file modification time changed since opening")
	}
	return nil
}
