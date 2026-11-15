package pcscommand

import (
	"errors"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/pcstable"
	"os"
	"path"
	"strconv"
)

// RunLocateDownload 执行获取直链
func RunLocateDownload(pcspaths []string, opt *LocateDownloadOption) {
	if err := RunLocateDownloadWithError(pcspaths, opt); err != nil {
		fmt.Println(err)
	}
}

// RunLocateDownloadWithError keeps available links and reports every failure.
func RunLocateDownloadWithError(pcspaths []string, opt *LocateDownloadOption) error {
	if opt == nil {
		opt = &LocateDownloadOption{}
	}

	absPaths, err := matchPathByShellPattern(pcspaths...)
	if err != nil {
		return err
	}
	if len(absPaths) == 0 {
		return ErrShellPatternNoHit
	}

	return locateResolvedPaths(GetBaiduPCS(), absPaths, opt)
}

func locateResolvedPaths(pcs *baidupcs.BaiduPCS, absPaths []string, opt *LocateDownloadOption) error {
	var failures []error

	if opt.FromPan {
		fds, err := pcs.FilesDirectoriesBatchMeta(absPaths...)
		if err != nil {
			return err
		}
		foundPaths := make(map[string]bool, len(fds))
		for _, fd := range fds {
			foundPaths[path.Join("/", fd.Path)] = true
		}
		for _, name := range absPaths {
			if !foundPaths[path.Join("/", name)] {
				failures = append(failures, fmt.Errorf("%s: 未获取到文件元信息", name))
			}
		}
		if len(fds) == 0 {
			return errors.Join(failures...)
		}

		fidList := make([]int64, 0, len(fds))
		for i := range fds {
			fidList = append(fidList, fds[i].FsID)
		}

		list, err := pcs.LocatePanAPIDownload(fidList...)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}

		tb := pcstable.NewTable(os.Stdout)
		tb.SetHeader([]string{"#", "fs_id", "路径", "链接"})

		links := make(map[int64]string, len(list))
		for _, link := range list {
			id, err := strconv.ParseInt(link.FsID, 10, 64)
			if err == nil && id > 0 && links[id] == "" {
				links[id] = link.Dlink
			}
		}
		rows := 0
		for k, id := range fidList {
			if link := links[id]; link != "" {
				tb.Append([]string{strconv.Itoa(k), strconv.FormatInt(id, 10), fds[k].Path, link})
				rows++
			} else {
				failures = append(failures, fmt.Errorf("%s (fs_id %d): 未获取到下载链接", fds[k].Path, id))
			}
		}
		if rows > 0 {
			tb.Render()
			fmt.Printf("\n注意: 以上链接不能直接访问, 需要登录百度帐号才可以下载\n")
		}
		return errors.Join(failures...)
	}

	rows := 0
	for i, pcspath := range absPaths {
		info, err := pcs.LocateDownload(pcspath)
		if err != nil {
			failures = append(failures, fmt.Errorf("[%d] %s: %w", i, pcspath, err))
			continue
		}
		urls := info.URLStrings(pcsconfig.Config.EnableHTTPS)
		if len(urls) == 0 {
			failures = append(failures, fmt.Errorf("%s: 未获取到有效下载链接", pcspath))
			continue
		}

		fmt.Printf("[%d] %s: \n", i, pcspath)
		tb := pcstable.NewTable(os.Stdout)
		tb.SetHeader([]string{"#", "链接"})
		for k, u := range urls {
			tb.Append([]string{strconv.Itoa(k), u.String()})
		}
		tb.Render()
		fmt.Println()
		rows++
	}
	if rows > 0 {
		fmt.Printf("提示: 访问下载链接, 需将下载器的 User-Agent 设置为: %s\n", pcsconfig.Config.PanUA)
	}
	return errors.Join(failures...)
}
