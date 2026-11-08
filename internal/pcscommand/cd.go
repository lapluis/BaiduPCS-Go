package pcscommand

import (
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

// RunChangeDirectory 执行更改工作目录
func RunChangeDirectory(targetPath string, isList bool) {
	if err := RunChangeDirectoryWithError(targetPath, isList); err != nil {
		fmt.Println(err)
	}
}

// RunChangeDirectoryWithError changes the saved directory or returns the failure.
func RunChangeDirectoryWithError(targetPath string, isList bool) error {
	pcs := GetBaiduPCS()
	err := matchPathByShellPatternOnce(&targetPath)
	if err != nil {
		return err
	}

	data, err := pcs.FilesDirectoriesMeta(targetPath)
	if err != nil {
		return err
	}

	if !data.Isdir {
		return fmt.Errorf("错误: %s 不是一个目录 (文件夹)", targetPath)
	}

	user := GetActiveUser()
	previous := user.Workdir
	user.Workdir = targetPath
	if err := pcsconfig.Config.Save(); err != nil {
		user.Workdir = previous
		return fmt.Errorf("保存工作目录 %s 失败: %w", targetPath, err)
	}

	fmt.Printf("改变工作目录: %s\n", targetPath)

	if isList {
		if err := RunLsWithError(".", nil, baidupcs.DefaultOrderOptions); err != nil {
			return fmt.Errorf("工作目录已切换至 %s, 列目录失败: %w", targetPath, err)
		}
	}
	return nil
}
