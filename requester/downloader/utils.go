package downloader

import (
	"github.com/qjfoidnh/BaiduPCS-Go/pcsverbose"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	mathrand "math/rand"
	"mime"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	// ContentRangeRE Content-Range 正则
	ContentRangeRE       = regexp.MustCompile(`^.*? \d*?-\d*?/(\d*?)$`)
	contentRangeBoundsRE = regexp.MustCompile(`(?i)^bytes ([0-9]+)-([0-9]+)/([0-9]+|\*)$`)

	// ranSource 随机数种子
	ranSource = mathrand.NewSource(time.Now().UnixNano())

	// ran 一个随机数实例
	ran = mathrand.New(ranSource)
)

func parseContentRangeBounds(value string) (begin, end, total int64, ok bool) {
	parts := contentRangeBoundsRE.FindStringSubmatch(strings.TrimSpace(value))
	if len(parts) != 4 {
		return
	}
	var err error
	if begin, err = strconv.ParseInt(parts[1], 10, 64); err != nil {
		return
	}
	if end, err = strconv.ParseInt(parts[2], 10, 64); err != nil || end < begin {
		return
	}
	total = -1
	if parts[3] != "*" {
		if total, err = strconv.ParseInt(parts[3], 10, 64); err != nil || total <= end {
			return
		}
	}
	ok = true
	return
}

// RandomNumber 生成指定区间随机数
func RandomNumber(min, max int) int {
	if min > max {
		min, max = max, min
	}
	return ran.Intn(max-min) + min
}

// GetFileName 获取文件名
func GetFileName(uri string, client *requester.HTTPClient) (filename string, err error) {
	if client == nil {
		client = requester.NewHTTPClient()
	}

	resp, err := client.Req("HEAD", uri, nil, nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return "", err
	}

	_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if err != nil {
		pcsverbose.Verbosef("DEBUG: GetFileName ParseMediaType error: %s\n", err)
		return path.Base(uri), nil
	}

	filename, err = url.QueryUnescape(params["filename"])
	if err != nil {
		return
	}

	if filename == "" {
		filename = path.Base(uri)
	}

	return
}

// ParseContentRange 解析Content-Range
func ParseContentRange(contentRange string) (contentLength int64) {
	_, _, total, ok := parseContentRangeBounds(contentRange)
	if !ok {
		return -1
	}
	return total
}

func fixCacheSize(size *int) {
	if *size < 1024 {
		*size = 1024
	}
}
