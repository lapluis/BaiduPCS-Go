package pcsupload

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

// A saved timestamp and length do not prove that the current bytes still match
// the saved session. Verify before using cached rapid-upload data or part IDs.
func (utu *UploadTaskUnit) verifiedResumeState(ctx context.Context) (*uploader.InstanceState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	local := utu.LocalFileChecksum
	providedDigest := len(local.MD5) == md5.Size
	state, err := utu.UploadingDatabase.SearchWithError(&local.LocalFileMeta)
	if err != nil || state == nil {
		return state, err
	}
	if len(local.MD5) == md5.Size {
		previous := append([]byte(nil), local.MD5...)
		if err := local.SumWithContext(ctx, checksum.CHECKSUM_MD5|checksum.CHECKSUM_SLICE_MD5); err != nil {
			local.MD5, local.SliceMD5, local.BlocksList = nil, nil, nil
			return nil, fmt.Errorf("verify upload resume source: %w", err)
		}
		if !bytes.Equal(previous, local.MD5) {
			// Discard chunk hashes belonging to the old file version. Lookup
			// with the freshly verified hash invalidates the mismatched record.
			local.BlocksList = nil
			state, err = utu.UploadingDatabase.SearchWithError(&local.LocalFileMeta)
			if err != nil || state == nil {
				return state, err
			}
		} else if !providedDigest {
			// The matching whole-file digest came from this saved record.
			return state, nil
		}
	}
	// No-rapid sessions have no whole-file digest. Check only completed parts
	// and retain the session, reuploading any part that no longer matches.
	buffer := make([]byte, 128*1024)
	for _, block := range state.BlockList {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if block == nil || block.Range.Begin < 0 || block.Range.End <= block.Range.Begin || block.Range.End > local.Length {
			return nil, uploader.ErrInvalidInstanceState
		}
		if block.CheckSum == "" {
			continue
		}
		hash := md5.New()
		length := block.Range.End - block.Range.Begin
		reader := &uploadResumeReader{ctx: ctx, reader: io.NewSectionReader(local.GetFile(), block.Range.Begin, length)}
		n, err := io.CopyBuffer(hash, reader, buffer)
		if err == nil && n != length {
			err = io.ErrUnexpectedEOF
		}
		if err != nil {
			return nil, fmt.Errorf("verify completed upload part %d: %w", block.ID, err)
		}
		if !strings.EqualFold(block.CheckSum, hex.EncodeToString(hash.Sum(nil))) {
			block.CheckSum = ""
		}
	}
	if err := local.ValidateSnapshot(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return state, nil
}

type uploadResumeReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *uploadResumeReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, errors.Join(err, canceled)
	}
	return n, err
}
