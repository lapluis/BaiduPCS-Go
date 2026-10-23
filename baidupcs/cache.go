package baidupcs

import (
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/expires"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	pathpkg "path"
	"strings"
	"time"
)

func directoryCachePath(dir string) string {
	if dir == "" {
		return PathSeparator
	}
	return pathpkg.Clean(dir)
}

type directoryCacheKey struct {
	path  string
	by    OrderBy
	order Order
}

// deleteCache 删除含有 dirs 的缓存
func (pcs *BaiduPCS) deleteCache(dirs []string, subtrees ...string) {
	pcs.lazyInit()
	changed := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		changed[directoryCachePath(dir)] = true
	}
	roots := make([]string, len(subtrees))
	for i, dir := range subtrees {
		roots[i] = directoryCachePath(dir)
	}
	pcs.cacheOpMap.InvalidateKeys(OperationFilesDirectoriesList, func(key interface{}) bool {
		if listing, ok := key.(directoryCacheKey); ok {
			remove := changed[listing.path]
			for _, root := range roots {
				if listing.path == root || strings.HasPrefix(listing.path, strings.TrimSuffix(root, "/")+"/") {
					remove = true
					break
				}
			}
			return remove
		}
		return false
	})
}

// CacheFilesDirectoriesList 缓存获取
func (pcs *BaiduPCS) CacheFilesDirectoriesList(path string, options *OrderOptions) (fdl FileDirectoryList, pcsError pcserror.Error) {
	pcs.lazyInit()
	path = directoryCachePath(path)
	if options == nil {
		options = DefaultOrderOptions
	}
	data := pcs.cacheOpMap.CacheOperation(OperationFilesDirectoriesList, directoryCacheKey{path, options.By, options.Order}, func() expires.DataExpires {
		fdl, pcsError = pcs.FilesDirectoriesList(path, options)
		if pcsError != nil {
			return nil
		}
		return expires.NewDataExpires(fdl, 1*time.Minute)
	})
	if pcsError != nil {
		return
	}
	return data.Data().(FileDirectoryList), nil
}

// CacheUK 缓存获取
func (pcs *BaiduPCS) CacheUK() (uk int64, pcsError pcserror.Error) {
	pcs.lazyInit()
	data := pcs.cacheOpMap.CacheOperation(OperationGetUK, pcs.GetBDUSS(), func() expires.DataExpires {
		uk, pcsError = pcs.UK()
		if pcsError != nil {
			return nil
		}
		return expires.NewDataExpires(uk, 24*time.Hour)
	})
	if pcsError != nil {
		return
	}
	return data.Data().(int64), nil
}
