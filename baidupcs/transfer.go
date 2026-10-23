package baidupcs

import (
	"encoding/json"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/tidwall/gjson"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type (
	// ShareOption 分享可选项
	TransferOption struct {
		Download bool   // 是否直接开始下载
		Collect  bool   // 多文件整合
		Rname    bool   // 随机改文件名
		FsID     string // 指定fs_id转存
	}
)

func (pcs *BaiduPCS) GenerateShareQueryURL(subPath string, params map[string]string) *url.URL {
	shareURL := &url.URL{
		Scheme: GetHTTPScheme(true),
		Host:   PanBaiduCom,
		Path:   "/share/" + subPath,
	}
	uv := shareURL.Query()
	for key, value := range params {
		uv.Set(key, value)
	}

	shareURL.RawQuery = uv.Encode()
	return shareURL
}

func shareResponseErrno(body []byte) (int64, error) {
	if !gjson.ValidBytes(body) {
		return 0, fmt.Errorf("返回json解析错误")
	}
	code := gjson.GetBytes(body, "errno")
	if code.Type != gjson.Number {
		return 0, fmt.Errorf("分享响应缺少有效的errno")
	}
	return strconv.ParseInt(code.Raw, 10, 64)
}

func (pcs *BaiduPCS) ExtractShareInfo(shareURL, shardID, shareUK, bdstoken string) (res map[string]string) {
	res = make(map[string]string)
	dataReadCloser, panError := pcs.sendReqReturnReadCloser(reqTypePan, OperationShareFileSavetoLocal, http.MethodGet, shareURL, nil, map[string]string{
		"User-Agent":   requester.UserAgent,
		"Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
	})
	if panError != nil {
		res["ErrMsg"] = "提交分享项查询请求时发生错误"
		return
	}
	defer dataReadCloser.Close()
	body, err := ioutil.ReadAll(dataReadCloser)
	if err != nil {
		res["ErrMsg"] = "读取分享项响应失败"
		return
	}
	errno, err := shareResponseErrno(body)
	if err != nil {
		res["ErrMsg"] = "分享项响应格式错误"
		return
	}
	if errno != 0 {
		res["ErrMsg"] = fmt.Sprintf("未知错误, 错误码%d", errno)
		if errno == 8001 {
			res["ErrMsg"] = "已触发验证, 请稍后再试"
		}
		return
	}
	list := gjson.GetBytes(body, "list")
	if !list.IsArray() || len(list.Array()) == 0 {
		res["ErrMsg"] = "分享项列表为空或格式错误"
		return
	}
	fsidList := make([]string, 0, len(list.Array()))
	for _, item := range list.Array() {
		sid := item.Get("fs_id")
		id, parseErr := strconv.ParseUint(sid.Raw, 10, 64)
		if sid.Type != gjson.Number || parseErr != nil || id == 0 || item.Get("server_filename").String() == "" {
			res["ErrMsg"] = "分享项元数据不完整"
			return
		}
		fsidList = append(fsidList, sid.Raw)
	}
	res["filename"] = list.Array()[0].Get("server_filename").String()

	res["shareid"] = shardID
	res["from"] = shareUK
	res["bdstoken"] = bdstoken
	shareUrl := &url.URL{
		Scheme: GetHTTPScheme(true),
		Host:   PanBaiduCom,
		Path:   "/share/transfer",
	}
	uv := shareUrl.Query()
	uv.Set("app_id", PanAppID)
	uv.Set("channel", "chunlei")
	uv.Set("clienttype", "0")
	uv.Set("web", "1")
	for key, value := range res {
		uv.Set(key, value)
	}
	res["item_num"] = strconv.Itoa(len(fsidList))
	res["ErrMsg"] = "success"
	res["fs_id"] = "[" + strings.Join(fsidList, ",") + "]"
	shareUrl.RawQuery = uv.Encode()
	res["shareUrl"] = shareUrl.String()
	return
}

func (pcs *BaiduPCS) PostShareQuery(url string, referer string, data map[string]string) (res map[string]string) {
	dataReadCloser, panError := pcs.sendReqReturnReadCloser(reqTypePan, OperationShareFileSavetoLocal, http.MethodPost, url, data, map[string]string{
		"User-Agent":   requester.UserAgent,
		"Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
		"Referer":      referer,
	})
	res = make(map[string]string)
	if panError != nil {
		res["ErrMsg"] = "提交分享项查询请求时发生错误"
		return
	}
	defer dataReadCloser.Close()
	body, err := ioutil.ReadAll(dataReadCloser)
	if err != nil {
		res["ErrMsg"] = "读取分享验证响应失败"
		return
	}
	errno, err := shareResponseErrno(body)
	if err != nil {
		res["ErrMsg"] = "分享验证响应格式错误"
		return
	}
	if errno != 0 {
		res["ErrMsg"] = fmt.Sprintf("未知错误, 错误码%d", errno)
		if errno == -9 {
			res["ErrMsg"] = "提取码错误"
		}
		return
	}
	res["randsk"] = gjson.Get(string(body), `randsk`).String()
	res["ErrMsg"] = "0"
	return
}

func (pcs *BaiduPCS) AccessSharePage(featurestr string, first bool) (tokens map[string]string) {
	tokens = make(map[string]string)
	tokens["ErrMsg"] = "0"
	if len(featurestr) < 2 || featurestr[0] != '1' {
		tokens["ErrMsg"] = "分享链接标识无效"
		return
	}
	headers := make(map[string]string)
	headers["User-Agent"] = NetdiskUA // 修 #534：Chrome 76 UA 被百度反爬弹到无 loginstate 的登录页；netdisk 客户端 UA 放行拿真实分享页
	headers["Referer"] = "https://pan.baidu.com/disk/home"
	if !first {
		headers["Referer"] = fmt.Sprintf("https://pan.baidu.com/share/init?surl=%s", featurestr[1:])
	}
	shareLink := fmt.Sprintf("https://pan.baidu.com/s/%s", featurestr)

	dataReadCloser, panError := pcs.sendReqReturnReadCloser(reqTypePan, OperationShareFileSavetoLocal, http.MethodGet, shareLink, nil, headers)

	if panError != nil {
		tokens["ErrMsg"] = "访问分享页失败"
		return
	}
	defer dataReadCloser.Close()
	body, readErr := ioutil.ReadAll(dataReadCloser)
	if readErr != nil {
		tokens["ErrMsg"] = "读取分享页失败"
		return
	}
	notFoundFlag := strings.Contains(string(body), "platform-non-found")
	errorPageTitle := strings.Contains(string(body), "error-404")
	if errorPageTitle {
		tokens["ErrMsg"] = "页面不存在"
		return
	}
	if notFoundFlag {
		tokens["ErrMsg"] = "分享链接已失效"
		return
	} else {
		re, _ := regexp.Compile(`(?s)(\{.+?loginstate.+?\})`) // 支持跨行页面数据以及旧版和新版脚本包装。
		sub := re.FindSubmatch(body)
		if len(sub) < 2 {
			tokens["ErrMsg"] = "请确认登录参数中已经包含了网盘STOKEN"
			return
		}
		tokens["bdstoken"] = gjson.Get(string(sub[1]), `bdstoken`).String()
		tokens["uk"] = gjson.Get(string(sub[1]), `uk`).String()
		tokens["share_uk"] = gjson.Get(string(sub[1]), `share_uk`).String()
		tokens["shareid"] = gjson.Get(string(sub[1]), `shareid`).String()
		return
	}

}

func (pcs *BaiduPCS) GenerateRequestQuery(mode string, params map[string]string) (res map[string]string) {
	res = make(map[string]string)
	res["ErrNo"] = "0"
	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/76.0.3809.100 Safari/537.36",
		"Referer":    params["referer"],
	}
	if mode == "POST" {
		headers["Content-Type"] = "application/x-www-form-urlencoded"
	}
	postdata := make(map[string]string)
	postdata["fsidlist"] = params["fs_id"]
	postdata["path"] = params["path"]
	dataReadCloser, panError := pcs.sendReqReturnReadCloser(reqTypePan, OperationShareFileSavetoLocal, mode, params["shareUrl"], postdata, headers)
	if panError != nil {
		res["ErrNo"] = "1"
		res["ErrMsg"] = "网络错误"
		return
	}
	defer dataReadCloser.Close()
	body, err := ioutil.ReadAll(dataReadCloser)
	if err != nil {
		res["ErrNo"] = "-1"
		res["ErrMsg"] = "未知错误"
		return
	}
	errno, err := shareResponseErrno(body)
	if err != nil {
		res["ErrNo"] = "2"
		res["ErrMsg"] = "返回json解析错误"
		return
	}
	if errno != 0 {
		res["ErrNo"] = "3"
		res["ErrMsg"] = "获取分享项元数据错误"
		if mode == "POST" && errno == 12 {
			path := gjson.Get(string(body), `info.0.path`).String()
			_, file := filepath.Split(path) // Should be path.Split here, but never mind~
			_errno := gjson.Get(string(body), `info.0.errno`).Int()
			targetFileNums := gjson.Get(string(body), `target_file_nums`).Int()
			targetFileNumsLimit := gjson.Get(string(body), `target_file_nums_limit`).Int()
			if targetFileNums > targetFileNumsLimit {
				res["ErrNo"] = "4"
				res["ErrMsg"] = fmt.Sprintf("转存文件数%d超过当前用户上限, 当前用户单次最大转存数%d", targetFileNums, targetFileNumsLimit)
				res["limit"] = fmt.Sprintf("%d", targetFileNumsLimit)
			} else if _errno == -30 {
				res["ErrNo"] = "9"
				res["ErrMsg"] = fmt.Sprintf("当前目录下已有%s同名文件/文件夹", file)
			} else {
				res["ErrMsg"] = fmt.Sprintf("未知错误, 错误代码%d", _errno)
			}
		} else if mode == "POST" && errno == 4 {
			res["ErrMsg"] = fmt.Sprintf("文件重复")
		}
		return
	}

	info := gjson.GetBytes(body, "info")
	if !info.IsArray() || len(info.Array()) == 0 {
		res["ErrNo"], res["ErrMsg"] = "2", "转存响应缺少文件信息"
		return
	}
	filenames := make([]string, 0, len(info.Array()))
	for _, item := range info.Array() {
		filePath := item.Get("path")
		name := path.Base(filePath.String())
		if filePath.Type != gjson.String || !path.IsAbs(filePath.String()) || name == "/" || name == "." || name == ".." {
			res["ErrNo"], res["ErrMsg"] = "2", "转存响应文件路径无效"
			return
		}
		filenames = append(filenames, name)
	}
	res["filename"] = filenames[0]
	// Keep the legacy display field, but use JSON for lossless filename transport.
	res["filenames"] = strings.Join(filenames, ",")
	encoded, _ := json.Marshal(filenames)
	res["filenames_json"] = string(encoded)
	if len(filenames) > 1 {
		res["filename"] += "等多个文件/文件夹"
	}
	return
}

func (pcs *BaiduPCS) SuperTransfer(params map[string]string, limit string) {
	//headers := map[string]string{
	//	"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/76.0.3809.100 Safari/537.36",
	//	"Referer":    params["referer"],
	//}
	//limit_num, _ := strconv.Atoi(limit)
	//fsidlist_str := params["fs_id"]
	//fsidlist := strings.Split(fsidlist_str[1:len(fsidlist_str)-1], ",")
	//listUrl := &url.URL{
	//	Scheme: GetHTTPScheme(true),
	//	Host:   PanBaiduCom,
	//	Path:   "/share/list",
	//}
	//uv := listUrl.Query()
	//uv.Set("app_id", PanAppID)
	//uv.Set("channel", "chunlei")
	//uv.Set("clienttype", "0")
	//uv.Set("web", "1")
	//uv.Set("page", "1")
	//uv.Set("num", "100")
	//uv.Set("shorturl", params["shorturl"])
	//uv.Set("root", "1")
	//dataReadCloser, panError := pcs.sendReqReturnReadCloser(reqTypePan, OperationShareFileSavetoLocal, http.MethodGet, listUrl.String(), nil, headers)
	//if panError != nil {
	//	res["ErrNo"] = "1"
	//	res["ErrMsg"] = "网络错误"
	//	return
	//}
	//defer dataReadCloser.Close()
	//body, err := ioutil.ReadAll(dataReadCloser)
	//res["ErrNo"] = "-1"
	//if err != nil {
	//	res["ErrMsg"] = "未知错误"
	//	return
	//}
	return

}
