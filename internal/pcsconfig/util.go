package pcsconfig

import (
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/converter"
	"strings"
)

// AverageParallel 返回平均的下载最大并发量
func AverageParallel(parallel, downloadLoad int) int {
	if downloadLoad < 1 {
		return 1
	}

	p := parallel / downloadLoad
	if p < 1 {
		return 1
	}
	return p
}

func stripPerSecond(sizeStr string) string {
	if len(sizeStr) >= 2 && strings.EqualFold(sizeStr[len(sizeStr)-2:], "/s") {
		return sizeStr[:len(sizeStr)-2]
	}
	return sizeStr
}

func showMaxRate(size int64) string {
	if size <= 0 {
		return "不限制"
	}
	return converter.ConvertFileSize(size, 2) + "/s"
}
