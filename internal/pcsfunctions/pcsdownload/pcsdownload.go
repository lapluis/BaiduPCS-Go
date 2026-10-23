package pcsdownload

import (
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/downloader"
	"net/http"
	"strconv"
)

// IsSkipMd5Checksum 是否忽略某些校验
func IsSkipMd5Checksum(size int64, md5Str string) bool {
	switch {
	case size == 1749504 && md5Str == "48bb9b0361dc9c672f3dc7b3ffcfde97": //8秒温馨提示
		fallthrough
	case size == 120 && md5Str == "6c1b84914588d09a6e5ec43605557457": //温馨提示文字版
		return true
	}
	return false
}

// BaiduPCSURLCheckFunc downloader 首次检查下载地址要执行的函数
func BaiduPCSURLCheckFunc(client *requester.HTTPClient, durl string) (contentLength int64, resp *http.Response, err error) {
	resp, err = client.Req(http.MethodGet, durl, nil, map[string]string{
		"Range": "bytes=0-" + strconv.FormatInt(baidupcs.InitRangeSize-1, 10),
	})
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return 0, nil, err
	}

	contentLengthStr := resp.Header.Get("x-bs-file-size")
	if contentLengthStr != "" {
		contentLength, err = strconv.ParseInt(contentLengthStr, 10, 64)
		if err != nil || contentLength < 0 {
			return 0, resp, fmt.Errorf("invalid x-bs-file-size %q", contentLengthStr)
		}
	}
	if resp.StatusCode == http.StatusPartialContent {
		total := downloader.ParseContentRange(resp.Header.Get("Content-Range"))
		if total < 0 || (contentLengthStr != "" && contentLength != total) {
			return 0, resp, fmt.Errorf("invalid or conflicting download Content-Range %q", resp.Header.Get("Content-Range"))
		}
		return total, resp, nil
	}
	if contentLengthStr != "" {
		if resp.StatusCode == http.StatusOK && ((resp.ContentLength >= 0 && contentLength != resp.ContentLength) || (contentLength == 0 && resp.ContentLength != 0)) {
			return 0, resp, fmt.Errorf("download size %d conflicts with Content-Length %d", contentLength, resp.ContentLength)
		}
		return contentLength, resp, nil
	}
	return resp.ContentLength, resp, nil
}
