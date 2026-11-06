package baidupcs

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

// Rename 重命名文件/目录
func (pcs *BaiduPCS) Rename(from, to string) (pcsError pcserror.Error) {
	return pcs.RenameWithContext(context.Background(), from, to)
}

// RenameWithContext cancels renaming requests and response decoding.
func (pcs *BaiduPCS) RenameWithContext(ctx context.Context, from, to string) (pcsError pcserror.Error) {
	return pcs.cpmvOp(ctx, OperationRename, &CpMvJSON{
		From: from,
		To:   to,
	})
}

// Copy 批量拷贝文件/目录
func (pcs *BaiduPCS) Copy(cpmvJSON ...*CpMvJSON) (pcsError pcserror.Error) {
	return pcs.CopyWithContext(context.Background(), cpmvJSON...)
}

// CopyWithContext cancels copying requests and response decoding.
func (pcs *BaiduPCS) CopyWithContext(ctx context.Context, cpmvJSON ...*CpMvJSON) (pcsError pcserror.Error) {
	return pcs.cpmvOp(ctx, OperationCopy, cpmvJSON...)
}

// Move 批量移动文件/目录
func (pcs *BaiduPCS) Move(cpmvJSON ...*CpMvJSON) (pcsError pcserror.Error) {
	return pcs.MoveWithContext(context.Background(), cpmvJSON...)
}

// MoveWithContext cancels moving requests and response decoding.
func (pcs *BaiduPCS) MoveWithContext(ctx context.Context, cpmvJSON ...*CpMvJSON) (pcsError pcserror.Error) {
	return pcs.cpmvOp(ctx, OperationMove, cpmvJSON...)
}

func (pcs *BaiduPCS) cpmvOp(ctx context.Context, op string, cpmvJSON ...*CpMvJSON) (pcsError pcserror.Error) {
	dataReadCloser, err := pcs.prepareCpMvOp(ctx, op, cpmvJSON...)
	defer pcs.invalidateCpMvCache(op, cpmvJSON)
	if err != nil {
		return err
	}

	defer dataReadCloser.Close()

	errInfo := pcserror.DecodePCSJSONError(op, dataReadCloser)
	if err := ctx.Err(); err != nil {
		cancelInfo := pcserror.NewPCSErrorInfo(op)
		cancelInfo.SetNetError(err)
		return cancelInfo
	}
	if errInfo != nil {
		return errInfo
	}

	return nil
}

// A failed request may already have reached the server. Invalidate affected
// listings even when a response is lost or the operation is canceled.
func (pcs *BaiduPCS) invalidateCpMvCache(op string, cpmvJSON []*CpMvJSON) {
	var subtrees []string
	entries := make(CpMvJSONList, 0, len(cpmvJSON))
	for _, entry := range cpmvJSON {
		if entry == nil {
			continue
		}
		entries = append(entries, entry)
		subtrees = append(subtrees, entry.To)
		if op != OperationCopy {
			subtrees = append(subtrees, entry.From)
		}
	}
	pcs.deleteCache(entries.AllRelatedDir(), subtrees...)
}
