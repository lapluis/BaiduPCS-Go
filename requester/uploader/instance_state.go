package uploader

import (
	"errors"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

// ErrInvalidInstanceState indicates that saved blocks cannot reconstruct the file.
var ErrInvalidInstanceState = errors.New("invalid upload resume state")

func validateUploadState(state *InstanceState, size int64) error {
	if size < 0 {
		return fmt.Errorf("%w: negative file size", ErrInvalidInstanceState)
	}
	if state == nil || len(state.BlockList) == 0 {
		return nil
	}
	byID := make([]*BlockState, len(state.BlockList))
	for _, block := range state.BlockList {
		if block == nil || block.ID < 0 || block.ID >= len(byID) {
			return fmt.Errorf("%w: missing block or invalid block ID", ErrInvalidInstanceState)
		}
		if byID[block.ID] != nil {
			return fmt.Errorf("%w: duplicate block ID %d", ErrInvalidInstanceState, block.ID)
		}
		byID[block.ID] = block
	}
	var end int64
	for _, block := range byID {
		if block == nil || block.Range.Begin != end || block.Range.End <= end || block.Range.End > size {
			return fmt.Errorf("%w: block ranges do not form a complete file", ErrInvalidInstanceState)
		}
		end = block.Range.End
	}
	if end != size {
		return fmt.Errorf("%w: block coverage ends at %d, file size is %d", ErrInvalidInstanceState, end, size)
	}
	return nil
}

type (
	// BlockState 文件区块信息
	BlockState struct {
		ID       int            `json:"id"`
		Range    transfer.Range `json:"range"`
		CheckSum string         `json:"checksum"`
	}

	// InstanceState 上传断点续传信息
	InstanceState struct {
		BlockList []*BlockState `json:"block_list"`
		Uploadid  string        `json:"uploadid"`
	}
)

func (muer *MultiUploader) getWorkerListByInstanceState(is *InstanceState) workerList {
	workers := make(workerList, 0, len(is.BlockList))
	for _, blockState := range is.BlockList {
		if blockState.CheckSum == "" {
			workers = append(workers, &worker{
				id:         blockState.ID,
				partOffset: blockState.Range.Begin,
				splitUnit:  NewBufioSplitUnit(muer.file, blockState.Range, muer.speedsStat, muer.rateLimit),
				checksum:   blockState.CheckSum,
			})
		} else {
			// 已经完成的, 也要加入 (可继续优化)
			workers = append(workers, &worker{
				id:         blockState.ID,
				partOffset: blockState.Range.Begin,
				splitUnit: &fileBlock{
					readRange: blockState.Range,
					readed:    blockState.Range.End - blockState.Range.Begin,
					readerAt:  muer.file,
				},
				checksum: blockState.CheckSum,
			})
		}
	}
	return workers
}
