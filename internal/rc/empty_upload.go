package rc

import (
	"context"
	"net/http"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsupload"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/multipartreader"
)

func uploadEmptyFile(ctx context.Context, pcs *baidupcs.BaiduPCS, target string, client *requester.HTTPClient) (uploadErr error) {
	defer func() { uploadErr = preserveContextError(ctx, uploadErr) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := pcs.PrepareUpload(baidupcs.OverWritePolicy, target, func(uri string, jar http.CookieJar) (*http.Response, error) {
		mr := multipartreader.NewMultipartReader()
		mr.AddFormFile("file", "empty", pcsupload.EmptyReaderLen64{})
		if err := mr.CloseMultipart(); err != nil {
			return nil, err
		}
		client.SetCookiejar(jar)
		return client.ReqWithContext(ctx, http.MethodPost, uri, mr, nil)
	})
	if err != nil {
		return err
	}
	defer body.Close()
	return pcserror.DecodePCSJSONError(baidupcs.OperationUpload, body)
}
