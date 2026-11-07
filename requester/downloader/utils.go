package downloader

import (
	"github.com/qjfoidnh/BaiduPCS-Go/pcsverbose"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	mathrand "math/rand/v2"
	"mime"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

var (
	// ContentRangeRE Content-Range 正则
	ContentRangeRE = regexp.MustCompile(`^.*? \d*?-\d*?/(\d*?)$`)
	// HTTP unit names are ASCII. Unicode case folding also accepts a long s.
	contentRangeBoundsRE = regexp.MustCompile(`^[bB][yY][tT][eE][sS] ([0-9]+)-([0-9]+)/([0-9]+|\*)$`)
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

func acceptsByteRanges(values ...string) bool {
	// Accept-Ranges is a list of case-insensitive ASCII unit names. Preserve
	// non-ASCII lookalikes instead of folding them into the bytes token.
	for _, value := range values {
		for _, unit := range strings.Split(value, ",") {
			if strings.ToLower(strings.Trim(unit, " \t")) == DefaultAcceptRanges {
				return true
			}
		}
	}
	return false
}

// RandomNumber returns a number in [min,max), swapping reversed bounds.
// Equal bounds return that value. It is safe for concurrent callers.
func RandomNumber(min, max int) int {
	if min > max {
		min, max = max, min
	}
	if min == max {
		return min
	}
	// Unsigned arithmetic supports intervals wider than the largest signed int.
	width := uint(max) - uint(min)
	return int(uint(min) + uint(mathrand.Uint64N(uint64(width))))
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

	_, params, parseErr := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if parseErr == nil && params["filename"] != "" {
		// ParseMediaType already decodes extended filename parameters. Normal
		// filename values contain literal percent signs and plus characters.
		return params["filename"], nil
	}
	if parseErr != nil {
		pcsverbose.Verbosef("DEBUG: GetFileName ParseMediaType error: %s\n", parseErr)
	}
	parsed, parseErr := url.Parse(uri)
	if parseErr != nil {
		return "", parseErr
	}
	if resp.Request != nil && resp.Request.URL != nil {
		parsed = resp.Request.URL
	}
	// URL.Path is decoded once and excludes query parameters and fragments.
	return path.Base(parsed.Path), nil
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
