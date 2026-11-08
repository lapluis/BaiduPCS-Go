package pcscommand

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcstable"
)

// RunShareSet 执行分享
func RunShareSet(paths []string, option *baidupcs.ShareOption) {
	pcspaths, err := matchPathByShellPattern(paths...)
	if err != nil {
		fmt.Println(err)
		return
	}

	shared, err := GetBaiduPCS().ShareSet(pcspaths, option)
	if err != nil {
		fmt.Printf("%s失败: %s\n", baidupcs.OperationShareSet, err)
		return
	}
	if option.IsCombined {
		fmt.Printf("shareID: %d, 链接: %s?pwd=%s\n", shared.ShareID, shared.Link, shared.Pwd)
	} else {
		fmt.Printf("shareID: %d, 链接: %s, 密码: %s\n", shared.ShareID, shared.Link, shared.Pwd)
	}
}

// RunShareCancel 执行取消分享
func RunShareCancel(shareIDs []int64) {
	if err := RunShareCancelWithError(shareIDs); err != nil {
		fmt.Println(err)
	}
}

// RunShareCancelWithError validates IDs and returns any cancellation failure.
func RunShareCancelWithError(shareIDs []int64) error {
	if len(shareIDs) == 0 {
		return fmt.Errorf("%s失败, 没有任何 shareid", baidupcs.OperationShareCancel)
	}
	for _, id := range shareIDs {
		if id <= 0 {
			return fmt.Errorf("shareid %d 必须为正整数", id)
		}
	}

	err := GetBaiduPCS().ShareCancel(shareIDs)
	if err != nil {
		return fmt.Errorf("%s失败: %w", baidupcs.OperationShareCancel, err)
	}

	fmt.Printf("%s成功\n", baidupcs.OperationShareCancel)
	return nil
}

// RunShareList 执行列出分享列表
func RunShareList(page int) {
	if page < 1 {
		page = 1
	}

	pcs := GetBaiduPCS()
	records, err := pcs.ShareList(page)
	if err != nil {
		fmt.Printf("%s失败: %s\n", baidupcs.OperationShareList, err)
		return
	}

	tb := pcstable.NewTable(os.Stdout)
	tb.SetHeader([]string{"#", "ShareID", "分享链接", "提取密码", "特征目录", "特征路径", "过期时间", "浏览次数"})
	for k, record := range records {
		if record.ExpireType == -1 {
			record.Valid = "已过期" // 已失效分享
		} else {
			if record.ExpireTime == 0 {
				record.Valid = "永久"
			} else {
				tm := time.Unix(time.Now().Unix()+record.ExpireTime, 0)
				record.Valid = tm.Format("2006/01/02 15:04:05")

			}

		}
		// 获取Passwd
		if record.Public == 0 && record.ExpireType != -1 {
			// 私密分享
			info, pcsError := pcs.ShareSURLInfo(record.ShareID)
			if pcsError != nil {
				// 获取错误
				fmt.Printf("[%d] 获取分享密码错误: %s\n", k, pcsError)
			} else {
				record.Passwd = strings.TrimSpace(info.Pwd)
			}
		}

		tb.Append([]string{strconv.Itoa(k), strconv.FormatInt(record.ShareID, 10), record.Shortlink, record.Passwd, path.Clean(path.Dir(record.TypicalPath)), record.TypicalPath, record.Valid, strconv.Itoa(record.ViewCount)})
	}
	tb.Render()
}
