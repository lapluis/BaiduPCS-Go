package pcsupdate

import (
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/cachepool"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
)

func newUpdateBuffer(size int64) (*rio.Buffer, error) {
	if size <= 0 || size > 0x7fffffff {
		return nil, fmt.Errorf("invalid update archive size: %d", size)
	}
	return rio.NewBuffer(cachepool.RawMallocByteSlice(int(size))), nil
}
