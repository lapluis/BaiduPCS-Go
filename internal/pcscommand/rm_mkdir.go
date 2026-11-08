package pcscommand

import (
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/pcstable"
	"os"
	"strconv"
)

// RunRemove 执行 批量删除文件/目录
func RunRemove(paths ...string) {
	if err := RunRemoveWithError(paths...); err != nil {
		fmt.Println(err)
	}
}

// RunRemoveWithError deletes matched paths and returns matching or API failures.
func RunRemoveWithError(paths ...string) error {
	paths, err := matchPathByShellPattern(paths...)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return ErrShellPatternNoHit
	}

	pnt := func() {
		tb := pcstable.NewTable(os.Stdout)
		tb.SetHeader([]string{"#", "文件/目录"})
		for k := range paths {
			tb.Append([]string{strconv.Itoa(k), paths[k]})
		}
		tb.Render()
	}

	err = GetBaiduPCS().Remove(paths...)
	if err != nil {
		fmt.Println("操作失败, 以下文件/目录删除失败: ")
		pnt()
		return err
	}

	fmt.Println("操作成功, 以下文件/目录已删除, 可在网盘文件回收站找回: ")
	pnt()
	return nil
}

// RunMkdir 执行 创建目录
func RunMkdir(path string) {
	if err := RunMkdirWithError(path); err != nil {
		fmt.Println(err)
	}
}

// RunMkdirWithError creates a directory and preserves the underlying API error.
func RunMkdirWithError(path string) error {
	activeUser := GetActiveUser()
	err := GetBaiduPCS().Mkdir(activeUser.PathJoin(path))
	if err != nil {
		return fmt.Errorf("创建目录 %s 失败: %w", path, err)
	}

	fmt.Println("创建目录成功:", path)
	return nil
}
