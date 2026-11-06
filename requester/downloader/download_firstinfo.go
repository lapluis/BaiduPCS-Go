package downloader

import (
	"fmt"
	"net/http"
	"reflect"
)

type (
	DownloadFirstInfo struct {
		ContentLength      int64
		ContentMD5         string
		ContentCRC32       string
		AcceptRanges       string
		Referer            string
		ETag, LastModified string
	}
)

func NewDownloadFirstInfoByResp(contentLength int64, resp *http.Response) (dfi *DownloadFirstInfo) {
	dfi = &DownloadFirstInfo{}
	if resp == nil {
		dfi.ContentLength = contentLength
		return
	}
	dfi.ContentLength = contentLength
	dfi.ContentMD5 = resp.Header.Get("Content-MD5")
	dfi.ContentCRC32 = resp.Header.Get("x-bs-meta-crc32")
	dfi.AcceptRanges = resp.Header.Get("Accept-Ranges")
	dfi.Referer = resp.Header.Get("Referer")
	dfi.ETag = resp.Header.Get("ETag")
	dfi.LastModified = resp.Header.Get("Last-Modified")
	return
}

func (dfi *DownloadFirstInfo) Compare(n *DownloadFirstInfo) bool {
	if n == nil {
		return false
	}
	if dfi.ContentLength != n.ContentLength {
		return false
	}
	if dfi.AcceptRanges != n.AcceptRanges {
		return false
	}
	if dfi.Referer != n.Referer {
		return false
	}
	if dfi.ETag != n.ETag || (dfi.ETag == "" && dfi.LastModified != n.LastModified) {
		return false
	}
	return true
}

// ToMap 转换为map
func (dfi *DownloadFirstInfo) ToMap() map[string]string {
	m := map[string]string{
		"Content-MD5":     dfi.ContentMD5,
		"x-bs-meta-crc32": dfi.ContentCRC32,
		"Accept-Ranges":   dfi.AcceptRanges,
		"Referer":         dfi.Referer,
	}
	// Use the same validator precedence as the actual range workers. Reject
	// a mirror here rather than treating its different entity as a later
	// source mutation that would unnecessarily reset a valid partial file.
	if dfi.ETag != "" {
		m["ETag"] = dfi.ETag
	} else if dfi.LastModified != "" {
		m["Last-Modified"] = dfi.LastModified
	}
	return m
}

// ToMapByReflect 用reflect转换为map
func (dfi *DownloadFirstInfo) ToMapByReflect() map[string]string {
	te := reflect.TypeOf(dfi).Elem()
	ve := reflect.ValueOf(dfi).Elem()
	n := te.NumField()
	m := map[string]string{}
	for i := 0; i < n; i++ {
		f := te.Field(i)
		m[f.Name] = fmt.Sprint(ve.Field(i).Interface())
	}
	return m
}
