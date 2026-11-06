package baidupcs

import (
	"context"
	"errors"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

type quotaInfo struct {
	*pcserror.PCSErrInfo
	Quota *int64 `json:"quota"`
	Used  *int64 `json:"used"`
}

// QuotaInfo 获取当前用户空间配额信息
func (pcs *BaiduPCS) QuotaInfo() (quota, used int64, pcsError pcserror.Error) {
	return pcs.QuotaInfoWithContext(context.Background())
}

// QuotaInfoWithContext cancels the request and response decoding.
func (pcs *BaiduPCS) QuotaInfoWithContext(ctx context.Context) (quota, used int64, pcsError pcserror.Error) {
	dataReadCloser, pcsError := pcs.PrepareQuotaInfoWithContext(ctx)
	if pcsError != nil {
		return
	}

	defer dataReadCloser.Close()

	quotaInfo := &quotaInfo{
		PCSErrInfo: pcserror.NewPCSErrorInfo(OperationQuotaInfo),
	}

	pcsError = pcserror.HandleJSONParse(OperationQuotaInfo, dataReadCloser, quotaInfo)
	if err := ctx.Err(); err != nil {
		quotaInfo.PCSErrInfo.SetNetError(err)
		return 0, 0, quotaInfo.PCSErrInfo
	}
	if pcsError != nil {
		return
	}

	if quotaInfo.Quota == nil || quotaInfo.Used == nil {
		quotaInfo.ErrType = pcserror.ErrTypeOthers
		quotaInfo.Err = errors.New("quota response is missing quota or used")
		return 0, 0, quotaInfo.PCSErrInfo
	}
	return *quotaInfo.Quota, *quotaInfo.Used, nil
}

// SpaceLeftInfo 获取当前用户剩余空间
func (pcs *BaiduPCS) SpaceLeftInfo() (free int64, pcsError pcserror.Error) {
	return pcs.SpaceLeftInfoWithContext(context.Background())
}

// SpaceLeftInfoWithContext computes remaining capacity within ctx.
func (pcs *BaiduPCS) SpaceLeftInfoWithContext(ctx context.Context) (free int64, pcsError pcserror.Error) {
	quota, used, pcsError := pcs.QuotaInfoWithContext(ctx)
	return quota - used, pcsError
}
