package baidupcs

import (
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/expires"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"time"
)

type directoryCacheKey struct {
	path  string
	by    OrderBy
	order Order
}

// deleteCache 删除含有 dirs 的缓存
func (pcs *BaiduPCS) deleteCache(dirs []string) {
	pcs.lazyInit()
	cache := pcs.cacheOpMap.LazyInitCachePoolOp(OperationFilesDirectoriesList)
	changed := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		changed[dir] = true
	}
	cache.Range(func(key interface{}, _ expires.DataExpires) bool {
		if listing, ok := key.(directoryCacheKey); ok && changed[listing.path] {
			cache.Delete(key)
		}
		return true
	})
}

// CacheFilesDirectoriesList 缓存获取
func (pcs *BaiduPCS) CacheFilesDirectoriesList(path string, options *OrderOptions) (fdl FileDirectoryList, pcsError pcserror.Error) {
	pcs.lazyInit()
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
