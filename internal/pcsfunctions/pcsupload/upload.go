package pcsupload

import (
	"context"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/multipartreader"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
	"io"
	"net/http"
	"sync"
	"time"
)

type (
	PCSUpload struct {
		pcs        *baidupcs.BaiduPCS
		targetPath string
	}

	EmptyReaderLen64 struct {
	}
)

var (
	uploadClient     *requester.HTTPClient
	uploadClientOnce sync.Once
)

func getUploadClient(jar http.CookieJar) *requester.HTTPClient {
	uploadClientOnce.Do(func() {
		uploadClient = pcsconfig.Config.PCSHTTPClient()
		uploadClient.SetTimeout(200 * time.Second)
	})
	// Keep the transport pooled, but never share mutable account state between
	// requests. Updating the global client's jar would race with other accounts.
	client := *uploadClient
	client.SetCookiejar(jar)
	return &client
}

var pcsPeriod = 256 // 上传多少个分片更换一次pcsHost

func (e EmptyReaderLen64) Read(p []byte) (n int, err error) {
	return 0, io.EOF
}

func (e EmptyReaderLen64) Len() int64 {
	return 0
}

func NewPCSUpload(pcs *baidupcs.BaiduPCS, targetPath string) uploader.MultiUpload {
	return &PCSUpload{
		pcs:        pcs,
		targetPath: targetPath,
	}
}

func (pu *PCSUpload) lazyInit() {
	if pu.pcs == nil {
		pu.pcs = &baidupcs.BaiduPCS{}
	}
}

// Precreate 上传前准备, 获取本次上传用的pcs服务器
func (pu *PCSUpload) Precreate(ctx context.Context) (originPCSHost string, pcsError pcserror.Error) {
	pu.lazyInit()
	originPCSHost = pu.pcs.GetPCSAddr()
	discoveryErr, newPCSHost := pu.pcs.GetRandomPCSHostWithContext(ctx)
	if ctx.Err() != nil {
		canceled := pcserror.NewPCSErrorInfo(baidupcs.OperationGetPCSServer)
		canceled.SetNetError(ctx.Err())
		return originPCSHost, canceled
	}
	_ = discoveryErr // Discovery is best effort; the original server remains usable.
	pu.pcs.SetPCSAddr(newPCSHost)
	return
}

func (pu *PCSUpload) TmpFile(ctx context.Context, uploadId, targetPath string, partSeq int, partOffset int64, r rio.ReaderLen64) (checksum string, uperr error) {
	pu.lazyInit()

	var respErr *uploader.MultiError

	// 每上传一定量分片切换一次动态pcs addr
	if partSeq%pcsPeriod == pcsPeriod-1 {
		pcsError, newPCSHost := pu.pcs.GetRandomPCSHostWithContext(ctx)
		if ctx.Err() != nil {
			canceled := pcserror.NewPCSErrorInfo(baidupcs.OperationUploadTmpFile)
			canceled.SetNetError(ctx.Err())
			return "", canceled
		}
		_ = pcsError
		pu.pcs.SetPCSAddr(newPCSHost)
	}

	checksum, pcsError := pu.pcs.UploadTmpFile(uploadId, targetPath, partSeq, partOffset, func(uploadURL string, jar http.CookieJar) (resp *http.Response, err error) {

		mr := multipartreader.NewMultipartReader()
		mr.AddFormFile("uploadedfile", "", r)
		mr.CloseMultipart()

		cli := getUploadClient(jar)
		resp, err = cli.ReqWithContext(ctx, http.MethodPost, uploadURL, mr, nil)
		if resp != nil {
			// 不可恢复的错误
			switch resp.StatusCode {
			case 400, 401, 403, 413: // 4xx通常是由客户端非法操作引发，直接深度重试
				respErr = &uploader.MultiError{
					Terminated: true,
					Err:        fmt.Errorf("upload returned HTTP status %d", resp.StatusCode),
				}
			}
		}
		return
	})

	if respErr != nil {
		if pcsError != nil {
			respErr.Err = pcsError
		}
		return checksum, respErr
	}

	return checksum, pcsError
}

func (pu *PCSUpload) CreateSuperFile(ctx context.Context, pcsHost, policy, uploadId string, fileSize int64, checksumMap map[int]string) (err error) {
	pu.lazyInit()
	pu.pcs.SetPCSAddr(pcsHost) // 恢复默认pcs服务器
	return pu.pcs.UploadCreateSuperFileWithContext(ctx, uploadId, policy, fileSize, pu.targetPath, checksumMap)
}
