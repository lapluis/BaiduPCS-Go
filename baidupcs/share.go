package baidupcs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

type (
	// ShareOption 分享可选项
	ShareOption struct {
		Password   string // 密码
		Period     int    // 有效期
		IsCombined bool   // 是否将密码输出到分享链接
	}

	// Shared 分享信息
	Shared struct {
		Link    string `json:"link"`
		Pwd     string `json:"pwd"`
		ShareID int64  `json:"shareid"`
	}

	// ShareRecordInfo 分享信息
	ShareRecordInfo struct {
		ShareID         int64   `json:"shareId"`
		FsIds           []int64 `json:"fsIds"`
		Passwd          string  `json:"-"` // 这个字段已经没有用了, 需要从ShareSURLInfo中获取
		Shortlink       string  `json:"shortlink"`
		Status          int     `json:"status"`          // 状态
		Public          int     `json:"public"`          // 是否为公开分享
		TypicalCategory int     `json:"typicalCategory"` // 文件类型
		TypicalPath     string  `json:"typicalPath"`     // 路径
		ExpireType      int     `json:"expiredType"`     // 过期类型
		ExpireTime      int64   `json:"expiredTime"`     // 过期时间
		ViewCount       int     `json:"vCnt"`            // 浏览次数
		Valid           string  // 是否过期
	}

	shareSURLInfo struct {
		*pcserror.PanErrorInfo
		*ShareSURLInfo
	}

	// ShareSURLInfo 分享的子信息
	ShareSURLInfo struct {
		Pwd      string `json:"pwd"` // 新密码
		ShortURL string `json:"shorturl"`
	}

	// ShareRecordInfoList 分享信息列表
	ShareRecordInfoList []*ShareRecordInfo

	sharePSetJSON struct {
		*Shared
		*pcserror.PanErrorInfo
	}

	shareListJSON struct {
		List shareListRecords `json:"list"`
		*pcserror.PanErrorInfo
	}
)

// Empty share lists may be encoded as an object by the remote API.
// Normalize that field during decoding so remote errors remain distinguishable.
type shareListRecords ShareRecordInfoList

func (list *shareListRecords) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &object); err != nil {
			return err
		}
		if len(object) != 0 {
			return errors.New("share list must be an array or empty object")
		}
		*list = shareListRecords{}
		return nil
	}
	var records ShareRecordInfoList
	if err := json.Unmarshal(data, &records); err != nil {
		return err
	}
	*list = shareListRecords(records)
	return nil
}

var (
	// ErrShareLinkNotFound 未找到分享链接
	ErrShareLinkNotFound = errors.New("未找到分享链接")
)

// ShareSet 分享文件
func (pcs *BaiduPCS) ShareSet(paths []string, option *ShareOption) (s *Shared, pcsError pcserror.Error) {
	return pcs.ShareSetWithContext(context.Background(), paths, option)
}

// ShareSetWithContext cancels creation requests and response decoding.
func (pcs *BaiduPCS) ShareSetWithContext(ctx context.Context, paths []string, option *ShareOption) (s *Shared, pcsError pcserror.Error) {
	if option == nil {
		errInfo := pcserror.NewPanErrorInfo(OperationShareSet)
		errInfo.ErrType = pcserror.ErrTypeOthers
		errInfo.Err = errors.New("share options must not be nil")
		return nil, errInfo
	}
	// The returned password must describe this request, even if the caller
	// reuses the options while the response is received or decoded.
	settings := *option
	if len(settings.Password) != 4 {
		settings.Password = CreatePasswd()
	}

	dataReadCloser, pcsError := pcs.PrepareSharePSetWithContext(ctx, paths, settings.Password, settings.Period)
	if pcsError != nil {
		return
	}

	defer dataReadCloser.Close()

	errInfo := pcserror.NewPanErrorInfo(OperationShareSet)
	jsonData := sharePSetJSON{
		Shared:       &Shared{},
		PanErrorInfo: errInfo,
	}

	pcsError = pcserror.HandleJSONParse(OperationShareSet, dataReadCloser, &jsonData)
	if err := ctx.Err(); err != nil {
		errInfo.SetNetError(err)
		return nil, errInfo
	}
	if pcsError != nil {
		return
	}

	if jsonData.Link == "" {
		errInfo.ErrType = pcserror.ErrTypeOthers
		errInfo.Err = ErrShareLinkNotFound
		return nil, errInfo
	}
	if jsonData.ShareID <= 0 {
		errInfo.ErrType = pcserror.ErrTypeOthers
		errInfo.Err = errors.New("invalid share creation result: share ID must be positive")
		return nil, errInfo
	}
	jsonData.Pwd = settings.Password

	return jsonData.Shared, nil
}

// ShareCancel 取消分享
func (pcs *BaiduPCS) ShareCancel(shareIDs []int64) (pcsError pcserror.Error) {
	return pcs.ShareCancelWithContext(context.Background(), shareIDs)
}

// ShareCancelWithContext cancels the request and response decoding.
func (pcs *BaiduPCS) ShareCancelWithContext(ctx context.Context, shareIDs []int64) (pcsError pcserror.Error) {
	dataReadCloser, pcsError := pcs.PrepareShareCancelWithContext(ctx, shareIDs)
	if pcsError != nil {
		return
	}

	defer dataReadCloser.Close()

	pcsError = pcserror.DecodePanJSONError(OperationShareCancel, dataReadCloser)
	if err := ctx.Err(); err != nil {
		errInfo := pcserror.NewPanErrorInfo(OperationShareCancel)
		errInfo.SetNetError(err)
		return errInfo
	}
	return
}

// ShareList 列出分享列表
func (pcs *BaiduPCS) ShareList(page int) (records ShareRecordInfoList, pcsError pcserror.Error) {
	return pcs.ShareListWithContext(context.Background(), page)
}

// ShareListWithContext cancels listing requests and response decoding.
func (pcs *BaiduPCS) ShareListWithContext(ctx context.Context, page int) (records ShareRecordInfoList, pcsError pcserror.Error) {
	dataReadCloser, pcsError := pcs.PrepareShareListWithContext(ctx, page)
	if pcsError != nil {
		return
	}

	defer dataReadCloser.Close()

	errInfo := pcserror.NewPanErrorInfo(OperationShareList)
	jsonData := shareListJSON{
		PanErrorInfo: errInfo,
	}

	pcsError = pcserror.HandleJSONParse(OperationShareList, dataReadCloser, &jsonData)
	if err := ctx.Err(); err != nil {
		errInfo.SetNetError(err)
		return nil, errInfo
	}
	if pcsError != nil {
		return
	}

	if jsonData.List == nil {
		errInfo.ErrType = pcserror.ErrTypeOthers
		errInfo.Err = errors.New("shared list is nil")
		return nil, errInfo
	}

	seen := make(map[int64]bool, len(jsonData.List))
	for index, record := range jsonData.List {
		if record == nil {
			errInfo.ErrType = pcserror.ErrTypeOthers
			errInfo.Err = fmt.Errorf("invalid share list: null entry at index %d", index)
			return nil, errInfo
		}
		if record.ShareID <= 0 || seen[record.ShareID] {
			errInfo.ErrType = pcserror.ErrTypeOthers
			errInfo.Err = fmt.Errorf("invalid share list: invalid or duplicate share ID at index %d", index)
			return nil, errInfo
		}
		seen[record.ShareID] = true
	}
	return ShareRecordInfoList(jsonData.List), nil
}

// ShareSURLInfo 获取分享的详细信息, 包含密码
func (pcs *BaiduPCS) ShareSURLInfo(shareID int64) (info *ShareSURLInfo, pcsError pcserror.Error) {
	return pcs.ShareSURLInfoWithContext(context.Background(), shareID)
}

// ShareSURLInfoWithContext cancels detail requests and response decoding.
func (pcs *BaiduPCS) ShareSURLInfoWithContext(ctx context.Context, shareID int64) (info *ShareSURLInfo, pcsError pcserror.Error) {
	dataReadCloser, pcsError := pcs.PrepareShareSURLInfoWithContext(ctx, shareID)
	if pcsError != nil {
		return
	}

	defer dataReadCloser.Close()

	errInfo := pcserror.NewPanErrorInfo(OperationShareSURLInfo)

	jsonData := shareSURLInfo{
		PanErrorInfo: errInfo,
	}

	pcsError = pcserror.HandleJSONParse(OperationShareSURLInfo, dataReadCloser, &jsonData)
	if err := ctx.Err(); err != nil {
		errInfo.SetNetError(err)
		return nil, errInfo
	}
	if pcsError != nil {
		// json解析错误
		return
	}
	if jsonData.ShareSURLInfo == nil {
		errInfo.ErrType = pcserror.ErrTypeOthers
		errInfo.Err = errors.New("share details are missing")
		return nil, errInfo
	}

	// 去掉0
	if jsonData.Pwd == "0" {
		jsonData.Pwd = ""
	}

	return jsonData.ShareSURLInfo, nil
}
