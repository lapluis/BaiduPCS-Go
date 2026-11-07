// Package pcserror PCS错误包
package pcserror

import (
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/jsonhelper"
	"io"
)

type (
	// ErrType 错误类型
	ErrType int

	// Error 错误信息接口
	Error interface {
		error
		SetJSONError(err error)
		SetNetError(err error)
		SetRemoteError()
		GetOperation() string
		GetErrType() ErrType
		GetRemoteErrCode() int
		GetRemoteErrMsg() string
		GetError() error
	}
)

const (
	// ErrorTypeNoError 无错误
	ErrorTypeNoError ErrType = iota
	// ErrTypeInternalError 内部错误
	ErrTypeInternalError
	// ErrTypeRemoteError 远端服务器返回错误
	ErrTypeRemoteError
	// ErrTypeNetError 网络错误
	ErrTypeNetError
	// ErrTypeJSONParseError json 数据解析失败
	ErrTypeJSONParseError
	// ErrTypeOthers 其他错误
	ErrTypeOthers
)

const (
	// StrSuccess 操作成功
	StrSuccess = "操作成功"
	// StrInternalError 内部错误
	StrInternalError = "内部错误"
	// StrRemoteError 远端服务器返回错误
	StrRemoteError = "远端服务器返回错误"
	// StrNetError 网络错误
	StrNetError = "网络错误"
	// StrJSONParseError json 数据解析失败
	StrJSONParseError = "json 数据解析失败"
)

// DecodePCSJSONError 解析PCS JSON的错误
func DecodePCSJSONError(operation string, data io.Reader) Error {
	errInfo := NewPCSErrorInfo(operation)
	response := struct {
		*PCSErrInfo
		ErrNo int `json:"errno"`
	}{PCSErrInfo: errInfo}
	if err := HandleJSONParse(operation, data, &response); err != nil {
		return errInfo
	}
	if response.ErrNo != 0 {
		alternate := NewPanErrorInfo(operation)
		alternate.ErrNo = response.ErrNo
		alternate.SetRemoteError()
		return alternate
	}
	return nil
}

// DecodePanJSONError 解析Pan JSON的错误
func DecodePanJSONError(operation string, data io.Reader) Error {
	errInfo := NewPanErrorInfo(operation)
	response := struct {
		*PanErrorInfo
		ErrCode int    `json:"error_code"`
		ErrMsg  string `json:"error_msg"`
	}{PanErrorInfo: errInfo}
	if err := HandleJSONParse(operation, data, &response); err != nil {
		return errInfo
	}
	if response.ErrCode != 0 {
		alternate := NewPCSErrorInfo(operation)
		alternate.ErrCode, alternate.ErrMsg = response.ErrCode, response.ErrMsg
		alternate.SetRemoteError()
		return alternate
	}
	return nil
}

// DecodeXPanJSONError 解析XPan JSON的错误
func DecodeXPanJSONError(operation string, data io.Reader) Error {
	errInfo := NewXPanErrorInfo(operation)
	response := struct {
		*XPanErrorInfo
		ErrCode int    `json:"error_code"`
		ErrMsg  string `json:"error_msg"`
	}{XPanErrorInfo: errInfo}
	err := HandleJSONParse(operation, data, &response)
	if err != nil && err.GetErrType() == ErrTypeJSONParseError {
		return errInfo
	}
	// An explicit native errno takes precedence. A synthetic return_type
	// error must not hide an explicit error from the alternate envelope.
	if errInfo.ErrNo == 0 && response.ErrCode != 0 {
		alternate := NewPCSErrorInfo(operation)
		alternate.ErrCode, alternate.ErrMsg = response.ErrCode, response.ErrMsg
		alternate.SetRemoteError()
		return alternate
	}
	if err != nil {
		return errInfo
	}
	return nil
}

// HandleJSONParse 处理解析json
func HandleJSONParse(op string, data io.Reader, info interface{}) (pcsError Error) {
	var (
		err     = jsonhelper.UnmarshalObjectData(data, info)
		errInfo = info.(Error)
	)

	if errInfo == nil {
		errInfo = NewPCSErrorInfo(op)
	}

	if err != nil {
		errInfo.SetJSONError(err)
		return errInfo
	}

	// 设置出错类型为远程错误
	if errInfo.GetRemoteErrCode() != 0 {
		errInfo.SetRemoteError()
		return errInfo
	}
	return nil
}
