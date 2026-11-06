package baidupcs

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"path"
)

// Remove 批量删除文件/目录
func (pcs *BaiduPCS) Remove(paths ...string) (pcsError pcserror.Error) {
	return pcs.RemoveWithContext(context.Background(), paths...)
}

// RemoveWithContext cancels deletion requests and response decoding.
func (pcs *BaiduPCS) RemoveWithContext(ctx context.Context, paths ...string) (pcsError pcserror.Error) {
	dataReadCloser, pcsError := pcs.PrepareRemoveWithContext(ctx, paths...)
	// The server may have applied the request even if its response is lost.
	defer pcs.deleteCache(allRelatedDir(paths), paths...)
	if pcsError != nil {
		return
	}

	defer dataReadCloser.Close()

	errInfo := pcserror.DecodePCSJSONError(OperationRemove, dataReadCloser)
	if err := ctx.Err(); err != nil {
		cancelInfo := pcserror.NewPCSErrorInfo(OperationRemove)
		cancelInfo.SetNetError(err)
		return cancelInfo
	}
	if errInfo != nil {
		return errInfo
	}

	return nil
}

// Mkdir 创建目录
func (pcs *BaiduPCS) Mkdir(pcspath string) (pcsError pcserror.Error) {
	return pcs.MkdirWithContext(context.Background(), pcspath)
}

// MkdirWithContext cancels creation requests and response decoding.
func (pcs *BaiduPCS) MkdirWithContext(ctx context.Context, pcspath string) (pcsError pcserror.Error) {
	dataReadCloser, pcsError := pcs.PrepareMkdirWithContext(ctx, pcspath)
	defer pcs.deleteCache([]string{path.Dir(directoryCachePath(pcspath))})
	if pcsError != nil {
		return
	}

	defer dataReadCloser.Close()

	errInfo := pcserror.DecodePCSJSONError(OperationMkdir, dataReadCloser)
	if err := ctx.Err(); err != nil {
		cancelInfo := pcserror.NewPCSErrorInfo(OperationMkdir)
		cancelInfo.SetNetError(err)
		return cancelInfo
	}
	if errInfo != nil {
		return errInfo
	}

	return
}
