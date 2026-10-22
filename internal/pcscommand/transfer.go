package pcscommand

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
)

var shareLinkRe = regexp.MustCompile(`(https?://pan\.baidu\.com/s/[0-9A-Za-z_-]+)(?:\?pwd=([0-9A-Za-z]{4}))?`)

// ShareTransferResult describes a completed share transfer.
type ShareTransferResult struct {
	Filename  string
	Filenames []string
}

// ShareTransferTo saves a share link into savePath using pcs. It returns errors
// instead of printing so that both the legacy command and pcsclone can report
// them with their own output and exit-code rules.
func ShareTransferTo(pcs *baidupcs.BaiduPCS, params []string, savePath string, opt *baidupcs.TransferOption) (*ShareTransferResult, error) {
	if len(params) == 0 || strings.TrimSpace(params[0]) == "" {
		return nil, errors.New("分享链接不能为空")
	}
	if len(params) > 2 {
		return nil, errors.New("参数过多: 需要分享链接和可选的提取码")
	}
	if opt == nil {
		opt = &baidupcs.TransferOption{}
	}

	link := params[0]
	// 支持整段分享文本(如 "通过网盘分享的文件: 链接: https://... 提取码: xxxx ..."),
	// 先从中提取出规范的分享链接, 再交给 net/url 解析
	if shareLinkRe.MatchString(link) {
		if m := shareLinkRe.FindStringSubmatch(link); len(m) >= 2 {
			link = m[1]
			if len(params) == 1 && len(m) == 3 && m[2] != "" {
				params = append(params, m[2])
			}
		}
	}
	parsedURL, err := url.Parse(link)
	if err != nil {
		return nil, errors.New("链接格式非法")
	}
	queryParams := parsedURL.Query()
	extraCode := queryParams.Get("pwd")
	if len(params) == 1 {
		if strings.Contains(link, "bdlink=") || !strings.Contains(link, "pan.baidu.com/") {
			return nil, errors.New("秒传已不再被支持")
		}
	} else {
		extraCode = params[1]
	}

	featureStr := path.Base(strings.TrimSuffix(parsedURL.Path, "/"))
	if featureStr == "init" {
		featureStr = "1" + queryParams.Get("surl")
	}
	if featureStr == "" || len(featureStr) > 23 || featureStr[0] != '1' || len(extraCode) != 4 {
		return nil, errors.New("链接地址或提取码非法")
	}

	tokens := pcs.AccessSharePage(featureStr, true)
	if tokens["ErrMsg"] != "0" {
		return nil, errors.New(tokens["ErrMsg"])
	}

	verifyURL := pcs.GenerateShareQueryURL("verify", map[string]string{
		"shareid":    tokens["shareid"],
		"time":       strconv.Itoa(int(time.Now().UnixMilli())),
		"clienttype": "1",
		"uk":         tokens["share_uk"],
	}).String()
	res := pcs.PostShareQuery(verifyURL, link, map[string]string{
		"pwd":       extraCode,
		"vcode":     "null",
		"vcode_str": "null",
		"bdstoken":  tokens["bdstoken"],
	})
	if res["ErrMsg"] != "0" {
		return nil, errors.New(res["ErrMsg"])
	}

	pcs.UpdatePCSCookies(true)

	tokens = pcs.AccessSharePage(featureStr, false)
	if tokens["ErrMsg"] != "0" {
		return nil, errors.New(tokens["ErrMsg"])
	}
	featureMap := map[string]string{
		"bdstoken": tokens["bdstoken"],
		"root":     "1",
		"web":      "5",
		"app_id":   baidupcs.PanAppID,
		"shorturl": featureStr[1:],
		"channel":  "chunlei",
	}
	queryShareInfoURL := pcs.GenerateShareQueryURL("list", featureMap).String()
	transMetas := pcs.ExtractShareInfo(queryShareInfoURL, tokens["shareid"], tokens["share_uk"], tokens["bdstoken"])
	if transMetas["ErrMsg"] != "success" {
		return nil, errors.New(transMetas["ErrMsg"])
	}

	// 如果指定了 fs_id, 覆盖解析结果
	if opt.FsID != "" {
		transMetas["fs_id"] = "[" + opt.FsID + "]"
		transMetas["filename"] = "user_specified_file" // 名字可能不准确，但不影响转存
		transMetas["item_num"] = "1"
	}

	savePath = path.Clean("/" + strings.TrimPrefix(savePath, "/"))
	if savePath == "/" {
		savePath = baidupcs.PathSeparator
	}
	transMetas["path"] = savePath
	if transMetas["item_num"] != "1" && opt.Collect {
		transMetas["filename"] += "等文件"
		transMetas["path"] = path.Join(savePath, transMetas["filename"])
		if err = pcs.Mkdir(transMetas["path"]); err != nil {
			// Re-transferring the same share may find the collect directory
			// already present; an existing directory is not an error.
			info, metaErr := pcs.FilesDirectoriesMeta(transMetas["path"])
			if metaErr != nil || info == nil || !info.Isdir {
				return nil, fmt.Errorf("创建汇总目录失败: %w", err)
			}
		}
	}
	transMetas["referer"] = "https://pan.baidu.com/s/" + featureStr
	pcs.UpdatePCSCookies(true)
	resp := pcs.GenerateRequestQuery("POST", transMetas)
	if resp["ErrNo"] != "0" {
		return nil, errors.New(resp["ErrMsg"])
	}
	if opt.Collect {
		resp["filename"] = transMetas["filename"]
	}

	result := &ShareTransferResult{Filename: resp["filename"]}
	if resp["filenames"] != "" {
		result.Filenames = strings.Split(resp["filenames"], ",")
	}
	if len(result.Filenames) == 0 && result.Filename != "" {
		result.Filenames = []string{result.Filename}
	}
	return result, nil
}

// RunShareTransfer 执行分享链接转存到网盘
func RunShareTransfer(params []string, opt *baidupcs.TransferOption) {
	result, err := ShareTransferTo(GetBaiduPCS(), params, GetActiveUser().Workdir, opt)
	if err != nil {
		fmt.Printf("%s失败: %s\n", baidupcs.OperationShareFileSavetoLocal, err)
		return
	}
	fmt.Printf("%s成功, 保存了%s到当前目录\n", baidupcs.OperationShareFileSavetoLocal, result.Filename)
	if opt != nil && opt.Download {
		fmt.Println("10s后开始下载")
		time.Sleep(10 * time.Second)
		RunDownload(result.Filenames, nil)
	}
}
