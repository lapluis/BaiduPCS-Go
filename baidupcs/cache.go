package baidupcs

import (
	"context"
	"errors"
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
	return pcs.CacheFilesDirectoriesListWithContext(context.Background(), path, options)
}

// CacheFilesDirectoriesListWithContext cancels cache waits and directory I/O.
func (pcs *BaiduPCS) CacheFilesDirectoriesListWithContext(ctx context.Context, path string, options *OrderOptions) (fdl FileDirectoryList, pcsError pcserror.Error) {
	pcs.lazyInit()
	path = directoryCachePath(path)
	if options == nil {
		options = DefaultOrderOptions
	}
	data, err := pcs.cacheOpMap.CacheOperationWithContext(ctx, OperationFilesDirectoriesList, directoryCacheKey{path, options.By, options.Order}, func() (expires.DataExpires, error) {
		fdl, pcsError = pcs.FilesDirectoriesListWithContext(ctx, path, options)
		if pcsError != nil {
			return nil, pcsError
		}
		return expires.NewDataExpires(fdl, 1*time.Minute), nil
	})
	if err != nil {
		return nil, cacheOperationError(OperationFilesDirectoriesList, err)
	}
	return data.Data().(FileDirectoryList), nil
}

// CacheUK 缓存获取
func (pcs *BaiduPCS) CacheUK() (uk int64, pcsError pcserror.Error) {
	return pcs.CacheUKWithContext(context.Background())
}

// CacheUKWithContext cancels cache waits and the account lookup.
func (pcs *BaiduPCS) CacheUKWithContext(ctx context.Context) (uk int64, pcsError pcserror.Error) {
	pcs.lazyInit()
	data, err := pcs.cacheOpMap.CacheOperationWithContext(ctx, OperationGetUK, pcs.GetBDUSS(), func() (expires.DataExpires, error) {
		uk, pcsError = pcs.UKWithContext(ctx)
		if pcsError != nil {
			return nil, pcsError
		}
		return expires.NewDataExpires(uk, 24*time.Hour), nil
	})
	if err != nil {
		return 0, cacheOperationError(OperationGetUK, err)
	}
	return data.Data().(int64), nil
}

func cacheOperationError(op string, err error) pcserror.Error {
	var pcsErr pcserror.Error
	if errors.As(err, &pcsErr) {
		return pcsErr
	}
	info := pcserror.NewPCSErrorInfo(op)
	info.SetNetError(err)
	return info
}
