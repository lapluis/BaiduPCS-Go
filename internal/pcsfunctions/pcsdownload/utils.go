package pcsdownload

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"golang.org/x/net/publicsuffix"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
)

// CheckFileValid 检测文件有效性
func CheckFileValid(filePath string, fileInfo *baidupcs.FileDirectory) error {
	return CheckFileValidWithContext(context.Background(), filePath, fileInfo)
}

// CheckFileValidWithContext allows cancellation while hashing downloaded data.
func CheckFileValidWithContext(ctx context.Context, filePath string, fileInfo *baidupcs.FileDirectory) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(fileInfo.BlockList) != 1 {
		return ErrDownloadNotSupportChecksum
	}

	f := checksum.NewLocalFileChecksum(filePath, int(baidupcs.SliceMD5Size))
	err := f.OpenPath()
	if err != nil {
		return err
	}
	defer f.Close()

	err = f.SumWithContext(ctx, checksum.CHECKSUM_MD5)
	if err != nil {
		return err
	}
	var expectedMD5 []byte
	validMD5 := len(fileInfo.MD5) == hex.EncodedLen(len(f.MD5))
	if validMD5 {
		var decodeErr error
		expectedMD5, decodeErr = hex.DecodeString(fileInfo.MD5)
		validMD5 = decodeErr == nil
	}
	if !validMD5 || !bytes.Equal(f.MD5, expectedMD5) {
		// 检测是否为违规文件
		if IsSkipMd5Checksum(f.Length, hex.EncodeToString(f.MD5)) {
			return ErrDownloadFileBanned
		}
		return ErrDownloadChecksumFailed
	}
	return nil
}

// FileExist reports a nonempty regular output without a checkpoint.
// Inspection errors are available through FileExistWithError.
func FileExist(path string) bool {
	exists, err := FileExistWithError(path)
	return exists && err == nil
}

// FileExistWithError distinguishes an absent checkpoint from a failed inspection.
func FileExistWithError(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, &os.PathError{Op: "inspect", Path: path, Err: errors.New("download output is not a regular file")}
	}
	if info.Size() == 0 {
		return false, nil
	}
	checkpoint := path + DownloadSuffix
	state, err := os.Stat(checkpoint)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !state.Mode().IsRegular() {
		return false, &os.PathError{Op: "inspect", Path: checkpoint, Err: errors.New("download checkpoint is not a regular file")}
	}
	return false, nil
}

// FixHTTPLinkURL 通过配置, 确定链接使用的协议(http,https)
func FixHTTPLinkURL(linkURL *url.URL) {
	if pcsconfig.Config.EnableHTTPS {
		if linkURL.Scheme == "http" {
			linkURL.Scheme = "https"
		}
	}
}

func CloneJarWithDomain(srcJar http.CookieJar, newURL string) (http.CookieJar, error) {
	sourceHost := pcsconfig.Config.PCSAddr
	if sourceHost == "" {
		sourceHost = baidupcs.PCSBaiduCom
	}
	return cloneJarFromHost(srcJar, sourceHost, newURL)
}

func cloneJarFromHost(srcJar http.CookieJar, sourceHost, newURL string) (http.CookieJar, error) {
	if srcJar == nil {
		return nil, fmt.Errorf("srcJar is nil")
	}
	dstJar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(newURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid download URL")
	}
	newDomain := u.Hostname()
	sourceURL, err := url.Parse("https://" + sourceHost + "/")
	if err != nil || sourceURL.Hostname() == "" || sourceURL.User != nil || sourceURL.Host != sourceHost {
		return nil, fmt.Errorf("invalid source PCS host")
	}
	cookies := srcJar.Cookies(sourceURL)
	cloneURL := &url.URL{Scheme: "https", Host: u.Host, Path: "/"}
	for _, c := range cookies {
		nc := *c
		nc.Domain = newDomain
		if net.ParseIP(newDomain) != nil {
			nc.Domain = ""
		}
		dstJar.SetCookies(cloneURL, []*http.Cookie{&nc})
	}
	return dstJar, nil
}
