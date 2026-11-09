package pcscommand

import (
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/converter"
)

// RunGetQuota 执行 获取当前用户空间配额信息, 并输出
func RunGetQuota() {
	if err := RunGetQuotaWithError(); err != nil {
		fmt.Println(err)
	}
}

// RunGetQuotaWithError reports request failures and handles zero capacity.
func RunGetQuotaWithError() error {
	quota, used, err := GetBaiduPCS().QuotaInfo()
	if err != nil {
		return err
	}
	ratio := "--"
	if quota > 0 {
		ratio = fmt.Sprintf("%f%%", 100*float64(used)/float64(quota))
	}
	fmt.Printf("用户名: %s, 总空间: %s, 已用空间: %s, 比率: %s\n",
		GetActiveUser().Name,
		converter.ConvertFileSize(quota),
		converter.ConvertFileSize(used),
		ratio,
	)
	return nil
}
