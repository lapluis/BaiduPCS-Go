package pcscommand

import (
	"fmt"
)

// RunGetMeta 执行 获取文件/目录的元信息
func RunGetMeta(targetPaths ...string) {
	if err := RunGetMetaWithError(targetPaths...); err != nil {
		fmt.Println(err)
	}
}

// RunGetMetaWithError retains earlier results but returns the first query failure.
func RunGetMetaWithError(targetPaths ...string) error {
	targetPaths, err := matchPathByShellPattern(targetPaths...)
	if err != nil {
		return err
	}
	if len(targetPaths) == 0 {
		return ErrShellPatternNoHit
	}

	for k, targetPath := range targetPaths {
		fmt.Printf("[%d] - [%s] --------------\n", k, targetPath)
		data, err := GetBaiduPCS().FilesDirectoriesMeta(targetPath)
		if err != nil {
			return fmt.Errorf("%s: %w", targetPath, err)
		}
		fmt.Println()
		fmt.Println(data)
	}
	return nil
}
