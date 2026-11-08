package pcsutil

import (
	"errors"
	"fmt"
	"github.com/kardianos/osext"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsverbose"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

func IsPipeInput() bool {
	fileInfo, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeNamedPipe) == os.ModeNamedPipe
}

// IsIPhoneOS 是否为苹果移动设备
func IsIPhoneOS() bool {
	if runtime.GOOS == "darwin" && (runtime.GOARCH == "arm" || runtime.GOARCH == "arm64") {
		_, err := os.Stat("Info.plist")
		return err == nil
	}
	return false
}

// ChWorkDir 切换回工作目录
func ChWorkDir() {
	if !IsIPhoneOS() {
		return
	}

	dir, err := filepath.Abs("")
	if err != nil {
		return
	}

	subPath := filepath.Dir(os.Args[0])
	os.Chdir(strings.TrimSuffix(dir, subPath))
}

// Executable 获取程序所在的真实目录或真实相对路径
func Executable() string {
	executablePath, err := osext.Executable()
	if err != nil {
		pcsverbose.Verbosef("DEBUG: osext.Executable: %s\n", err)
		executablePath, err = filepath.Abs(filepath.Dir(os.Args[0]))
		if err != nil {
			pcsverbose.Verbosef("DEBUG: filepath.Abs: %s\n", err)
			executablePath = filepath.Dir(os.Args[0])
		}
	}

	if IsIPhoneOS() {
		executablePath = filepath.Join(strings.TrimSuffix(executablePath, os.Args[0]), filepath.Base(os.Args[0]))
	}

	// 读取链接
	linkedExecutablePath, err := filepath.EvalSymlinks(executablePath)
	if err != nil {
		pcsverbose.Verbosef("DEBUG: filepath.EvalSymlinks: %s\n", err)
		return executablePath
	}
	return linkedExecutablePath
}

// ExecutablePath 获取程序所在目录
func ExecutablePath() string {
	return filepath.Dir(Executable())
}

// ExecutablePathJoin 返回程序所在目录的子目录
func ExecutablePathJoin(subPath string) string {
	return filepath.Join(ExecutablePath(), subPath)
}

// WalkDir 获取指定目录及所有子目录下的所有文件，可以匹配后缀过滤。
// 支持 Linux/macOS 软链接
func WalkDir(dirPth, suffix string) (files []string, err error) {
	return walkDir(dirPth, suffix, os.Stat, os.ReadDir)
}

func walkDir(dirPth, suffix string, stat func(string) (os.FileInfo, error), readDir func(string) ([]os.DirEntry, error)) (files []string, err error) {
	files = make([]string, 0, 32)
	suffix = strings.ToUpper(suffix) //忽略后缀匹配的大小写

	// Only ancestors are active: independent aliases of a directory must each
	// retain their logical upload paths, while a link back to an ancestor fails.
	var ancestors []os.FileInfo
	var walk func(string, os.FileInfo) error
	walk = func(filename string, info os.FileInfo) error {
		if info.Mode()&os.ModeSymlink != 0 {
			target, statErr := stat(filename)
			if statErr != nil {
				if errors.Is(statErr, os.ErrNotExist) { // Preserve skipping broken links.
					return nil
				}
				return statErr
			}
			info = target
		}
		if info.IsDir() {
			for _, ancestor := range ancestors {
				if os.SameFile(ancestor, info) {
					return &os.PathError{Op: "walk", Path: filename, Err: fmt.Errorf("directory link cycle")}
				}
			}
			ancestors = append(ancestors, info)
			defer func() { ancestors = ancestors[:len(ancestors)-1] }()
			entries, err := readDir(filename)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				childInfo, err := entry.Info()
				if err != nil {
					return err
				}
				if err := walk(filepath.Join(filename, entry.Name()), childInfo); err != nil {
					return err
				}
			}
			return nil
		}
		if info.Size() != 0 && strings.HasSuffix(strings.ToUpper(filepath.Base(filename)), suffix) {
			files = append(files, path.Clean(filename))
		}
		return nil
	}

	info, err := os.Lstat(dirPth)
	if err != nil {
		return files, err
	}
	err = walk(dirPth, info)
	return files, err
}

// ConvertToUnixPathSeparator 将 windows 目录分隔符转换为 Unix 的
func ConvertToUnixPathSeparator(p string) string {
	return strings.Replace(p, "\\", "/", -1)
}

func ChPathLegal(p string) bool {
	illegal_chars := "<>|:\"*?,\\"
	if runtime.GOOS == "windows" {
		illegal_chars = "<>|\"*?,\\"
	}

	if strings.ContainsAny(p, illegal_chars) {
		return false
	}
	return true
}
