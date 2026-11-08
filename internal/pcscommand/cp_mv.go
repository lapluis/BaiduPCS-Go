package pcscommand

import (
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"path"
	"strings"
)

// RunCopy 执行 批量拷贝文件/目录
func RunCopy(paths ...string) {
	if err := RunCopyWithError(paths...); err != nil {
		fmt.Println(err)
	}
}

// RunCopyWithError copies paths and returns validation or remote failures.
func RunCopyWithError(paths ...string) error { return runCpMvOp("copy", paths...) }

// RunMove 执行 批量 重命名/移动 文件/目录
func RunMove(paths ...string) {
	if err := RunMoveWithError(paths...); err != nil {
		fmt.Println(err)
	}
}

// RunMoveWithError moves paths and returns validation or remote failures.
func RunMoveWithError(paths ...string) error { return runCpMvOp("move", paths...) }

func runCpMvOp(op string, paths ...string) error {
	err := cpmvPathValid(paths...) // 检查路径的有效性, 目前只是判断数量
	if err != nil {
		return fmt.Errorf("%s path error: %w", op, err)
	}

	from, to := cpmvParsePath(paths...) // 分割

	from, err = matchPathByShellPattern(from...)
	if err != nil {
		return err
	}
	if len(from) == 0 {
		return ErrShellPatternNoHit
	}
	to = GetActiveUser().PathJoin(to)

	// 尝试匹配
	if strings.ContainsAny(to, baidupcs.ShellPatternCharacters) {
		tos, err := matchPathByShellPattern(to)
		if err != nil {
			return err
		}

		switch len(tos) {
		case 0:
			// do nothing
		case 1:
			to = tos[0]
		default:
			return fmt.Errorf("目标目录有 %d 条匹配结果, 请检查通配符", len(tos))
		}
	}

	pcs := GetBaiduPCS()
	toInfo, pcsError := pcs.FilesDirectoriesMeta(to)
	switch {
	case pcsError != nil && pcsError.GetErrType() == pcserror.ErrTypeRemoteError &&
		(pcsError.GetRemoteErrCode() == 31066 || pcsError.GetRemoteErrCode() == -9):
		// 判断路径是否存在
		// 如果不存在, 则为重命名或同目录拷贝操作

		// 如果 from 数不是1, 则意义不明确.
		if len(from) != 1 {
			return fmt.Errorf("目标 %s 不存在, 多个源路径需要已存在的目标目录: %w", to, pcsError)
		}

		if op == "copy" { // 拷贝
			err = pcs.Copy(&baidupcs.CpMvJSON{
				From: from[0],
				To:   to,
			})
			if err != nil {
				fmt.Println("文件/目录拷贝失败: ")
				fmt.Printf("%s <-> %s\n", from[0], to)
				return err
			}
			fmt.Println("文件/目录拷贝成功: ")
			fmt.Printf("%s <-> %s\n", from[0], to)
		} else { // 重命名
			err = pcs.Rename(from[0], path.Clean(to))
			if err != nil {
				fmt.Println("重命名失败: ")
				fmt.Printf("%s -> %s\n", from[0], to)
				return err
			}
			fmt.Println("重命名成功: ")
			fmt.Printf("%s -> %s\n", from[0], to)
		}
		return nil
	case pcsError != nil:
		return pcsError
	}

	if toInfo == nil {
		return fmt.Errorf("目标 %s 的元数据为空", to)
	}
	if !toInfo.Isdir {
		return fmt.Errorf("目标 %s 不是一个目录, 操作失败", toInfo.Path)
	}

	cj := new(baidupcs.CpMvListJSON)
	cj.List = make([]*baidupcs.CpMvJSON, len(from))
	for k := range from {
		cj.List[k] = &baidupcs.CpMvJSON{
			From: from[k],
			To:   path.Clean(to + baidupcs.PathSeparator + path.Base(from[k])),
		}
	}

	switch op {
	case "copy":
		err = pcs.Copy(cj.List...)
		if err != nil {
			fmt.Println("操作失败, 以下文件/目录拷贝失败: ")
			fmt.Println(cj)
			return err
		}
		fmt.Println("操作成功, 以下文件/目录拷贝成功: ")
		fmt.Println(cj)
	case "move":
		err = pcs.Move(cj.List...)
		if err != nil {
			fmt.Println("操作失败, 以下文件/目录移动失败: ")
			fmt.Println(cj)
			return err
		}
		fmt.Println("操作成功, 以下文件/目录移动成功: ")
		fmt.Println(cj)
	default:
		panic("Unknown operation:" + op)
	}
	return nil
}

// cpmvPathValid 检查路径的有效性
func cpmvPathValid(paths ...string) (err error) {
	if len(paths) <= 1 {
		return fmt.Errorf("参数不完整")
	}

	return nil
}

// cpmvParsePath 解析路径
func cpmvParsePath(paths ...string) (from []string, to string) {
	if len(paths) == 0 {
		return nil, ""
	}
	from = paths[:len(paths)-1]
	to = paths[len(paths)-1]
	return
}
