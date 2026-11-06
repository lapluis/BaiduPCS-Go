package pcsdownload

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"net/url"
)

func GetLocateDownloadLinks(pcs *baidupcs.BaiduPCS, pcspath string) (dlinks []*url.URL, err error) {
	return GetLocateDownloadLinksWithContext(context.Background(), pcs, pcspath)
}

func GetLocateDownloadLinksWithContext(ctx context.Context, pcs *baidupcs.BaiduPCS, pcspath string) (dlinks []*url.URL, err error) {
	dInfo, pcsError := pcs.LocateDownloadWithContext(ctx, pcspath)
	if pcsError != nil {
		return nil, pcsError
	}

	us := dInfo.URLStrings(pcsconfig.Config.EnableHTTPS)
	if len(us) == 0 {
		return nil, ErrDlinkNotFound
	}

	return us, nil
}
