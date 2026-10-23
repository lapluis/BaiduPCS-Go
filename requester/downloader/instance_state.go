package downloader

import (
	"errors"
	"fmt"
	"github.com/golang/protobuf/proto"
	"github.com/json-iterator/go"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/cachepool"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsverbose"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
	"os"
	"sort"
	"sync"
)

// ErrResumeFileMismatch means local data cannot support the saved progress.
var ErrResumeFileMismatch = errors.New("local file does not match download resume progress")

func (der *Downloader) validateResumeFile(info *transfer.DownloadInstanceInfo) error {
	file, ok := der.writer.(interface{ Stat() (os.FileInfo, error) })
	if !ok || der.config.IsTest {
		return nil
	}
	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrResumeFileMismatch, err)
	}
	total := info.DownloadStatus.TotalSize()
	if stat.Size() > total {
		return fmt.Errorf("%w: local=%d, remote=%d", ErrResumeFileMismatch, stat.Size(), total)
	}
	generated := total
	if gen := info.DownloadStatus.RangeListGen(); gen != nil && gen.RangeGenMode() == transfer.RangeGenMode_BlockSize {
		generated = gen.LoadBegin()
	}
	if generated < 0 || generated > total {
		return fmt.Errorf("%w: invalid generated extent", ErrResumeFileMismatch)
	}
	ranges := append(transfer.RangeList(nil), info.Ranges...)
	for _, r := range ranges {
		if r == nil || r.LoadBegin() < 0 || r.LoadBegin() > r.LoadEnd() || r.LoadEnd() > generated {
			return fmt.Errorf("%w: invalid pending range", ErrResumeFileMismatch)
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].LoadBegin() < ranges[j].LoadBegin() })
	// Every assigned byte beyond local EOF must still be pending. A gap means
	// the saved progress would skip completed data that is no longer present.
	covered := stat.Size()
	for _, r := range ranges {
		if r.LoadBegin() > covered {
			break
		}
		if r.LoadEnd() > covered {
			covered = r.LoadEnd()
		}
	}
	if covered < generated {
		return fmt.Errorf("%w: completed bytes extend beyond local size %d", ErrResumeFileMismatch, stat.Size())
	}
	return nil
}

type (
	//InstanceState 状态, 断点续传信息
	InstanceState struct {
		saveFile *os.File
		format   InstanceStateStorageFormat
		ii       transfer.DownloadInstanceInfoExporter
		mu       sync.Mutex
	}

	// InstanceStateStorageFormat 断点续传储存类型
	InstanceStateStorageFormat int
)

const (
	// InstanceStateStorageFormatJSON json 格式
	InstanceStateStorageFormatJSON = iota
	// InstanceStateStorageFormatProto3 protobuf 格式
	InstanceStateStorageFormatProto3
)

//NewInstanceState 初始化InstanceState
func NewInstanceState(saveFile *os.File, format InstanceStateStorageFormat) *InstanceState {
	return &InstanceState{
		saveFile: saveFile,
		format:   format,
	}
}

func (is *InstanceState) checkSaveFile() bool {
	return is.saveFile != nil
}

func (is *InstanceState) getSaveFileContents() []byte {
	if !is.checkSaveFile() {
		return nil
	}

	finfo, err := is.saveFile.Stat()
	if err != nil {
		panic(err)
	}

	size := finfo.Size()
	if size > 0xffffffff {
		panic("savePath too large")
	}
	intSize := int(size)

	buf := cachepool.RawMallocByteSlice(intSize)

	n, _ := is.saveFile.ReadAt(buf, 0)
	return buf[:n]
}

//Get 获取断点续传信息
func (is *InstanceState) Get() (eii *transfer.DownloadInstanceInfo) {
	if !is.checkSaveFile() {
		return nil
	}

	is.mu.Lock()
	defer is.mu.Unlock()

	contents := is.getSaveFileContents()
	if len(contents) <= 0 {
		return
	}

	is.ii = &transfer.DownloadInstanceInfoExport{}
	var err error
	switch is.format {
	case InstanceStateStorageFormatProto3:
		err = proto.Unmarshal(contents, is.ii.(*transfer.DownloadInstanceInfoExport))
	default:
		err = jsoniter.Unmarshal(contents, is.ii)
	}

	if err != nil {
		pcsverbose.Verbosef("DEBUG: InstanceInfo unmarshal error: %s\n", err)
		return
	}

	eii = is.ii.GetInstanceInfo()
	return
}

//Put 提交断点续传信息
func (is *InstanceState) Put(eii *transfer.DownloadInstanceInfo) {
	if !is.checkSaveFile() {
		return
	}

	is.mu.Lock()
	defer is.mu.Unlock()

	if is.ii == nil {
		is.ii = &transfer.DownloadInstanceInfoExport{}
	}
	is.ii.SetInstanceInfo(eii)
	var (
		data []byte
		err  error
	)
	switch is.format {
	case InstanceStateStorageFormatProto3:
		data, err = proto.Marshal(is.ii.(*transfer.DownloadInstanceInfoExport))
	default:
		data, err = jsoniter.Marshal(is.ii)
	}
	if err != nil {
		panic(err)
	}

	err = is.saveFile.Truncate(int64(len(data)))
	if err != nil {
		pcsverbose.Verbosef("DEBUG: truncate file error: %s\n", err)
	}

	_, err = is.saveFile.WriteAt(data, 0)
	if err != nil {
		pcsverbose.Verbosef("DEBUG: write instance state error: %s\n", err)
	}
}

//Close 关闭
func (is *InstanceState) Close() error {
	if !is.checkSaveFile() {
		return nil
	}

	return is.saveFile.Close()
}

func (der *Downloader) initInstanceState(format InstanceStateStorageFormat) (err error) {
	if der.instanceState != nil {
		return errors.New("already initInstanceState")
	}

	var saveFile *os.File
	if !der.config.IsTest && der.config.InstanceStatePath != "" {
		saveFile, err = os.OpenFile(der.config.InstanceStatePath, os.O_RDWR|os.O_CREATE, 0777)
		if err != nil {
			return err
		}
	}

	der.instanceState = NewInstanceState(saveFile, format)
	return nil
}

func (der *Downloader) removeInstanceState() error {
	der.instanceState.Close()
	if !der.config.IsTest && der.config.InstanceStatePath != "" {
		return os.Remove(der.config.InstanceStatePath)
	}
	return nil
}
