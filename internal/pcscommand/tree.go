package pcscommand

import (
	"errors"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"strings"
)

const (
	indentPrefix   = "│   "
	pathPrefix     = "├──"
	lastFilePrefix = "└──"
)

type (
	TreeOptions struct {
		Depth    int
		ShowFsid bool
	}
)

func getTree(pcspath string, depth int, option *TreeOptions) error {
	var (
		err   error
		files baidupcs.FileDirectoryList
	)
	if depth == 0 {
		err := matchPathByShellPatternOnce(&pcspath)
		if err != nil {
			return fmt.Errorf("%s: %w", pcspath, err)
		}
	}

	files, err = GetBaiduPCS().FilesDirectoriesList(pcspath, baidupcs.DefaultOrderOptions)
	if err != nil {
		return fmt.Errorf("%s: %w", pcspath, err)
	}

	var (
		prefix          = pathPrefix
		fN              = len(files)
		indentPrefixStr = strings.Repeat(indentPrefix, depth)
	)
	var failures []error
	for i, file := range files {
		if file.Isdir {
			if option.ShowFsid {
				fmt.Printf("%v%v %v/: %v\n", indentPrefixStr, pathPrefix, file.Filename, file.FsID)
			} else {
				fmt.Printf("%v%v %v/\n", indentPrefixStr, pathPrefix, file.Filename)
			}
			if option.Depth < 0 || depth < option.Depth {
				if err := getTree(file.Path, depth+1, option); err != nil {
					failures = append(failures, err)
				}
			}
			continue
		}

		if i+1 == fN {
			prefix = lastFilePrefix
		}
		if option.ShowFsid {
			fmt.Printf("%v%v %v: %v\n", indentPrefixStr, prefix, file.Filename, file.FsID)
		} else {
			fmt.Printf("%v%v %v\n", indentPrefixStr, prefix, file.Filename)
		}
	}

	return errors.Join(failures...)
}

// RunTree 列出树形图
func RunTree(path string, depth int, option *TreeOptions) {
	if err := RunTreeWithError(path, depth, option); err != nil {
		fmt.Println(err)
	}
}

// RunTreeWithError retains partial output and every failed directory's cause.
func RunTreeWithError(path string, depth int, option *TreeOptions) error {
	if depth < 0 {
		return fmt.Errorf("目录树起始深度不能为负数: %d", depth)
	}
	if option == nil {
		option = &TreeOptions{Depth: -1}
	}
	return getTree(path, depth, option)
}
