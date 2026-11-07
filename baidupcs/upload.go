package baidupcs

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/converter"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	// MaxUploadBlockSize 上传的文件分片最大大小
	MaxUploadBlockSize = 64 * converter.MB
	// MiddleUploadBlockSize 上传的文件分片中等大小
	MiddleUploadBlockSize = 16 * converter.MB
	// MinUploadBlockSize 上传的文件分片最小大小
	MinUploadBlockSize = 4 * converter.MB
	// RecommendedUploadSize 推荐的最高文件上传大小
	RecommendedUploadSize = 32 * converter.GB
	// MaxUploadSize 目前支持的最大文件大小
	MaxUploadSize = 128 * converter.GB
	// SliceMD5Size 计算 slice-md5 所需的长度
	SliceMD5Size = 256 * converter.KB
	// EmptyContentMD5 空串的md5
	EmptyContentMD5 = "d41d8cd98f00b204e9800998ecf8427e"
	// MiddleUploadThreshold 中等分片对应的文件大小
	MiddleUploadThreshold = 8 * converter.GB
	// MaxUploadThreshold 最大分片对应的文件大小
	MaxUploadThreshold = 32 * converter.GB
	// MinCheckLeftSpaceThreshold 需要检查剩余空间是否足够的最小文件大小
	MinCheckLeftSpaceThreshold = 64 * converter.MB
)

var (
	// ErrUploadMD5NotFound 未找到md5
	ErrUploadMD5NotFound = errors.New("unknown response data, md5 not found")
	// ErrUploadMD5Invalid indicates a malformed digest in a part response.
	ErrUploadMD5Invalid = errors.New("invalid upload response md5")
	// ErrUploadPrecreateInvalid means the response cannot start or finish an upload.
	ErrUploadPrecreateInvalid = errors.New("invalid upload precreate response")
	// ErrUploadSavePathFound 未找到保存路径
	ErrUploadSavePathFound = errors.New("unknown response data, file saved path not found")
	// ErrUploadSeqNotMatch 服务器返回的上传队列不匹配
	ErrUploadSeqNotMatch = errors.New("服务器返回的上传队列不匹配")
	// ErrUploadMD5Unknown 服务器无匹配文件/秒传未生效
	ErrUploadMD5Unknown = errors.New("服务器无匹配文件/秒传未生效")
	// ErrUploadFileExists 文件或目录已存在
	ErrUploadFileExists = errors.New("文件已存在")

	// FakeBlockListMD5 虚假秒传时的BlockList
	fakeBlockListMD5 = []string{"5910a591dd8fc18c32a8f3df4fdc1761", "a5fc157d78e6ad1c7e114b056c92821e"}
)

type (
	// UploadFunc 上传文件处理函数
	UploadFunc func(uploadURL string, jar http.CookieJar) (resp *http.Response, err error)

	// RapidUploadInfo 文件秒传信息
	RapidUploadInfo struct {
		Filename      string
		ContentLength int64
		ContentMD5    string
		SliceMD5      string
		ContentCrc32  string
	}

	uploadTmpFileJSON struct {
		MD5 string `json:"md5"`
		*pcserror.PCSErrInfo
	}

	uploadPrecreateJSON struct {
		ReturnType int    `json:"return_type"` // 1上传, 2秒传
		UploadID   string `json:"uploadid"`
		//BlockList  []int  `json:"block_list"`
		*pcserror.PanErrorInfo
		fdJSON `json:"info"`
	}

	// UploadSeq 分片上传顺序
	UploadSeq struct {
		Seq   int
		Block string
	}

	// PrecreateInfo 预提交文件消息返回数据
	PrecreateInfo struct {
		IsRapidUpload bool
		UploadID      string
		UploadSeqList []*UploadSeq
	}

	PCSServer struct {
		ServerAddr string `json:"server"`
	}

	PCSInfo struct {
		*pcserror.PCSErrInfo
		Host    string       `json:"host"`
		Server  []string     `json:"server"`
		Servers []*PCSServer `json:"servers"`
	}
)

// RapidUpload 秒传文件
func (pcs *BaiduPCS) RapidUpload(targetPath, policy, uploadid, contentMD5, sliceMD5, dataContent, crc32 string, offset, length, totalSize, dataTime int64, blockListMD5 []string) (pcsError pcserror.Error, jsonData uploadPrecreateJSON) {
	return pcs.RapidUploadWithContext(context.Background(), targetPath, policy, uploadid, contentMD5, sliceMD5, dataContent, crc32, offset, length, totalSize, dataTime, blockListMD5)
}

// RapidUploadWithContext keeps rapid-upload requests and decoding cancellable.
func (pcs *BaiduPCS) RapidUploadWithContext(ctx context.Context, targetPath, policy, uploadid, contentMD5, sliceMD5, dataContent, crc32 string, offset, length, totalSize, dataTime int64, blockListMD5 []string) (pcsError pcserror.Error, jsonData uploadPrecreateJSON) {
	defer pcs.deleteCache([]string{path.Dir(targetPath)})
	pcsError, jsonData = pcs.rapidUploadV2(ctx, targetPath, policy, uploadid, strings.ToLower(contentMD5), strings.ToLower(sliceMD5), dataContent, crc32, offset, length, totalSize, dataTime, blockListMD5)
	return
}

// FakeRapidUpload 只precreate不进行秒传
func (pcs *BaiduPCS) FakeRapidUpload(targetPath, policy string, length int64) (pcsError pcserror.Error, jsonData uploadPrecreateJSON) {
	return pcs.FakeRapidUploadWithContext(context.Background(), targetPath, policy, length)
}

// FakeRapidUploadWithContext keeps the full no-rapid handshake cancellable.
func (pcs *BaiduPCS) FakeRapidUploadWithContext(ctx context.Context, targetPath, policy string, length int64) (pcsError pcserror.Error, jsonData uploadPrecreateJSON) {
	defer pcs.deleteCache([]string{path.Dir(targetPath)})
	if length <= MinUploadBlockSize {
		pcsError, jsonData = pcs.fakeRapidUploadV2(ctx, targetPath, policy, length, time.Now().Unix(), fakeBlockListMD5[0:1])
		return
	}
	pcsError, jsonData = pcs.fakeRapidUploadV2(ctx, targetPath, policy, length, time.Now().Unix(), fakeBlockListMD5)
	return
}

func (pcs *BaiduPCS) rapidUploadV2(ctx context.Context, targetPath, policy, uploadid, contentMD5, sliceMD5, dataContent, crc32 string, offset, length, totalSize, dataTime int64, blockListMD5 []string) (pcsError pcserror.Error, jsonData uploadPrecreateJSON) {
	dataReadCloser, pcsError := pcs.PrepareRapidUploadV2WithContext(ctx, targetPath, policy, uploadid, contentMD5, sliceMD5, dataContent, crc32, offset, length, totalSize, dataTime, blockListMD5)
	if pcsError != nil {
		return
	}
	defer dataReadCloser.Close()
	jsonData = uploadPrecreateJSON{
		PanErrorInfo: pcserror.NewPanErrorInfo(OperationRapidUpload),
	}
	pcsError = pcserror.HandleJSONParse(OperationUpload, dataReadCloser, &jsonData)
	if err := ctx.Err(); err != nil {
		jsonData.PanErrorInfo.SetNetError(err)
		pcsError = jsonData.PanErrorInfo
	} else if pcsError == nil {
		pcsError = jsonData.validate(uploadid, true)
	}
	return pcsError, jsonData
}

func (pcs *BaiduPCS) fakeRapidUploadV2(ctx context.Context, targetPath, policy string, length, dateTime int64, blockListMD5 []string) (pcsError pcserror.Error, jsonData uploadPrecreateJSON) {
	dataReadCloser, pcsError := pcs.PrepareFakeRapidUploadV2WithContext(ctx, targetPath, policy, length, dateTime, blockListMD5)
	if pcsError != nil {
		return
	}
	defer dataReadCloser.Close()
	jsonData = uploadPrecreateJSON{
		PanErrorInfo: pcserror.NewPanErrorInfo(OperationRapidUpload),
	}
	pcsError = pcserror.HandleJSONParse(OperationUpload, dataReadCloser, &jsonData)
	if err := ctx.Err(); err != nil {
		jsonData.PanErrorInfo.SetNetError(err)
		pcsError = jsonData.PanErrorInfo
	} else if pcsError == nil {
		pcsError = jsonData.validate("", false)
	}
	return pcsError, jsonData
}

func (info *uploadPrecreateJSON) validate(existingSession string, allowRapid bool) pcserror.Error {
	if info.ReturnType == 2 && allowRapid {
		return nil
	}
	if info.ReturnType == 1 {
		session := info.UploadID
		if session == "" {
			session = existingSession
		}
		if strings.TrimSpace(session) != "" {
			info.UploadID = session
			return nil
		}
	}
	info.PanErrorInfo.ErrType = pcserror.ErrTypeInternalError
	info.PanErrorInfo.Err = ErrUploadPrecreateInvalid
	return info.PanErrorInfo
}

// RapidUploadNoCheckDir 秒传文件, 不进行目录检查, 会覆盖掉同名的目录!
func (pcs *BaiduPCS) RapidUploadNoCheckDir(targetPath, contentMD5, sliceMD5, crc32 string, length int64) (pcsError pcserror.Error) {
	// This API can replace a directory, so invalidate its cached subtree too.
	defer pcs.deleteCache([]string{path.Dir(targetPath)}, targetPath)
	dataReadCloser, pcsError := pcs.prepareRapidUpload(targetPath, contentMD5, sliceMD5, crc32, length)
	if pcsError != nil {
		return
	}

	defer dataReadCloser.Close()

	pcsError = pcserror.DecodePCSJSONError(OperationRapidUpload, dataReadCloser)
	if pcsError != nil {
		return
	}

	return nil
}

// UploadTmpFile 分片上传—文件分片及上传
func (pcs *BaiduPCS) UploadTmpFile(uploadid, targetPath string, partseq int, partOffset int64, uploadFunc UploadFunc) (md5 string, pcsError pcserror.Error) {
	dataReadCloser, pcsError := pcs.PrepareUploadSuperfile2(uploadid, targetPath, partseq, partOffset, uploadFunc)
	if pcsError != nil {
		return "", pcsError
	}

	defer dataReadCloser.Close()

	// 数据处理
	jsonData := uploadTmpFileJSON{
		PCSErrInfo: pcserror.NewPCSErrorInfo(OperationUploadTmpFile),
	}

	pcsError = pcserror.HandleJSONParse(OperationUploadTmpFile, dataReadCloser, &jsonData)
	if pcsError != nil {
		return
	}

	// 未找到md5
	if jsonData.MD5 == "" {
		jsonData.PCSErrInfo.ErrType = pcserror.ErrTypeInternalError
		jsonData.PCSErrInfo.Err = ErrUploadMD5NotFound
		return "", jsonData.PCSErrInfo
	}
	validMD5 := false
	if len(jsonData.MD5) == 32 {
		_, err := hex.DecodeString(jsonData.MD5)
		validMD5 = err == nil
	}
	if !validMD5 {
		jsonData.PCSErrInfo.ErrType = pcserror.ErrTypeInternalError
		jsonData.PCSErrInfo.Err = ErrUploadMD5Invalid
		return "", jsonData.PCSErrInfo
	}

	return jsonData.MD5, nil
}

// UploadCreateSuperFile 分片上传—合并分片文件
func (pcs *BaiduPCS) UploadCreateSuperFile(uploadid, policy string, fileSize int64, targetPath string, checksumMap map[int]string) (panError pcserror.Error) {
	return pcs.UploadCreateSuperFileWithContext(context.Background(), uploadid, policy, fileSize, targetPath, checksumMap)
}

// UploadCreateSuperFileWithContext merges uploaded parts using the caller's context.
func (pcs *BaiduPCS) UploadCreateSuperFileWithContext(ctx context.Context, uploadid, policy string, fileSize int64, targetPath string, checksumMap map[int]string) (panError pcserror.Error) {
	// A lost response does not prove that the server skipped the mutation.
	defer pcs.deleteCache([]string{path.Dir(targetPath)})
	blockList := sortBlockList(checksumMap)
	rtype := pcs.policyTortype(policy)
	dataReadCloser, pcsError := pcs.PrepareUploadCreateSuperFileWithContext(ctx, uploadid, rtype, fileSize, targetPath, blockList)
	if pcsError != nil {
		return pcsError
	}

	defer dataReadCloser.Close()

	errInfo := pcserror.DecodePanJSONError(OperationUploadCreateSuperFile, dataReadCloser)
	if ctx.Err() != nil {
		canceled := pcserror.NewPanErrorInfo(OperationUploadCreateSuperFile)
		canceled.SetNetError(ctx.Err())
		return canceled
	}
	if errInfo != nil {
		return errInfo
	}

	return nil
}

// GetRandomPCSHost 随机获取一个可用的pcs地址
func (pcs *BaiduPCS) GetRandomPCSHost() (pcsError pcserror.Error, pcsHost string) {
	return pcs.GetRandomPCSHostWithContext(context.Background())
}

// GetRandomPCSHostWithContext discovers a server without detaching cancellation.
func (pcs *BaiduPCS) GetRandomPCSHostWithContext(ctx context.Context) (pcsError pcserror.Error, pcsHost string) {
	if ctx.Err() != nil {
		canceled := pcserror.NewPCSErrorInfo(OperationGetPCSServer)
		canceled.SetNetError(ctx.Err())
		return canceled, ""
	}
	if pcs.fixPCSAddr {
		return
	}
	dataReadCloser, pcsError := pcs.PreparePCSServersWithContext(ctx)
	if pcsError != nil {
		return
	}
	defer dataReadCloser.Close()
	pcsInfo := &PCSInfo{
		PCSErrInfo: pcserror.NewPCSErrorInfo(OperationGetPCSServer),
	}
	pcsError = pcserror.HandleJSONParse(OperationGetPCSServer, dataReadCloser, pcsInfo)
	if ctx.Err() != nil {
		pcsInfo.SetNetError(ctx.Err())
		return pcsInfo.PCSErrInfo, ""
	}
	if pcsError != nil {
		return
	}
	pcsHostList := make([]string, 0)
	if len(pcsInfo.Servers) > 0 {
		for _, server := range pcsInfo.Servers {
			if server == nil {
				continue
			}
			if parsedURL := parseDownloadURL(server.ServerAddr, pcs.isHTTPS); parsedURL != nil {
				if host := uploadServerAuthority(parsedURL.Host); host != "" {
					pcsHostList = append(pcsHostList, host)
				}
			}
		}
	}
	if len(pcsHostList) == 0 && len(pcsInfo.Server) > 0 {
		for _, server := range pcsInfo.Server {
			if host := uploadServerAuthority(server); host != "" {
				pcsHostList = append(pcsHostList, host)
			}
		}
	}
	pcsHost = RandomElement(pcsHostList)
	return
}

// Preserve host ports and IPv6 brackets without admitting URL paths, user
// information or malformed addresses into URL.Host. Legacy entries are bare
// authorities; the servers list has already been parsed as HTTP(S) URLs.
func uploadServerAuthority(server string) string {
	parsed, err := url.Parse("//" + server)
	if err != nil || parsed.Host != server || !validServerAuthority(parsed) {
		return ""
	}
	return server
}
