package downloader

import (
	"errors"
	"fmt"
	"github.com/golang/protobuf/proto"
	"github.com/json-iterator/go"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsverbose"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// ErrResumeFileMismatch means local data cannot support the saved progress.
var ErrResumeFileMismatch = errors.New("local file does not match download resume progress")

// ErrCheckpointSave identifies persistence failures even when joined with cancellation.
var ErrCheckpointSave = errors.New("download resume checkpoint could not be saved")

func (der *Downloader) validateResumeFile(info *transfer.DownloadInstanceInfo) error {
	file, ok := der.writer.(interface{ Stat() (os.FileInfo, error) })
	if !ok || der.config.IsTest {
		return nil
	}
	stat, err := file.Stat()
	if err != nil {
		// Failure to inspect the file is not evidence that saved bytes are
		// missing. Keep the cause and avoid requesting destructive resume reset.
		return fmt.Errorf("inspect resumed download file: %w", err)
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
		filename string
		lease    *os.File
		initErr  error
		closed   bool
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

// NewInstanceState takes ownership of saveFile. Close the state, including
// read-only users; Load and Put report any ownership-acquisition failure.
func NewInstanceState(saveFile *os.File, format InstanceStateStorageFormat) *InstanceState {
	state := &InstanceState{
		saveFile: saveFile,
		format:   format,
	}
	if saveFile != nil {
		state.claimCheckpoint()
	}
	return state
}

func (is *InstanceState) checkSaveFile() bool {
	is.mu.Lock()
	defer is.mu.Unlock()
	return is.saveFile != nil
}

const maxCheckpointSize int64 = 16 << 20

// getSaveFileContents requires is.mu to remain held across file operations.
func (is *InstanceState) getSaveFileContents() ([]byte, error) {
	if is.saveFile == nil {
		return nil, nil
	}
	if err := is.verifyCheckpointLocked(); err != nil {
		return nil, err
	}

	finfo, err := is.saveFile.Stat()
	if err != nil {
		return nil, err
	}

	size := finfo.Size()
	if size < 0 || size > maxCheckpointSize {
		return nil, fmt.Errorf("%w: checkpoint size %d exceeds limit", ErrResumeFileMismatch, size)
	}
	buf := make([]byte, int(size))
	n, err := is.saveFile.ReadAt(buf, 0)
	if err != nil {
		return nil, err
	}
	if n != len(buf) {
		return nil, io.ErrUnexpectedEOF
	}
	return buf, nil
}

// Get retains the legacy optional-state interface. Use Load to handle errors.
func (is *InstanceState) Get() *transfer.DownloadInstanceInfo {
	info, err := is.Load()
	if err != nil {
		pcsverbose.Verbosef("DEBUG: checkpoint load error: %s\n", err)
	}
	return info
}

// Load reads and validates a checkpoint before constructing its range generator.
func (is *InstanceState) Load() (*transfer.DownloadInstanceInfo, error) {
	is.mu.Lock()
	defer is.mu.Unlock()

	contents, err := is.getSaveFileContents()
	if err != nil {
		return nil, err
	}
	if len(contents) <= 0 {
		return nil, nil
	}

	exported := &transfer.DownloadInstanceInfoExport{}
	switch is.format {
	case InstanceStateStorageFormatProto3:
		err = proto.Unmarshal(contents, exported)
	default:
		err = jsoniter.Unmarshal(contents, exported)
	}

	if err != nil {
		return nil, fmt.Errorf("%w: malformed checkpoint: %w", ErrResumeFileMismatch, err)
	}
	if err := validateCheckpoint(exported); err != nil {
		return nil, err
	}
	is.ii = exported
	return exported.GetInstanceInfo(), nil
}

func validateCheckpoint(info *transfer.DownloadInstanceInfoExport) error {
	bad := func(reason string) error { return fmt.Errorf("%w: %s", ErrResumeFileMismatch, reason) }
	if info.TotalSize < 0 || info.GenBegin < 0 || info.GenBegin > info.TotalSize {
		return bad("invalid generated extent")
	}
	generated := info.TotalSize
	switch info.RangeGenMode {
	case transfer.RangeGenMode_Default:
	case transfer.RangeGenMode_BlockSize:
		if info.BlockSize <= 0 {
			return bad("invalid block size")
		}
		generated = info.GenBegin
	default:
		return bad("unknown range generation mode")
	}
	ranges := append(transfer.RangeList(nil), info.Ranges...)
	for _, r := range ranges {
		if r == nil || r.Begin < 0 || r.Begin > r.End || r.End > generated {
			return bad("invalid pending range")
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Begin < ranges[j].Begin })
	var end int64
	for _, r := range ranges {
		if r.Begin == r.End {
			continue
		}
		if r.Begin < end {
			return bad("overlapping pending ranges")
		}
		end = r.End
	}
	return nil
}

// Put 提交断点续传信息
func (is *InstanceState) Put(eii *transfer.DownloadInstanceInfo) (saveErr error) {
	defer func() {
		if saveErr != nil {
			saveErr = fmt.Errorf("%w: %w", ErrCheckpointSave, saveErr)
		}
	}()

	is.mu.Lock()
	defer is.mu.Unlock()
	if is.saveFile == nil {
		return nil
	}
	if err := is.verifyCheckpointLocked(); err != nil {
		return err
	}
	if eii == nil || eii.DownloadStatus == nil {
		return fmt.Errorf("%w: missing checkpoint snapshot or status", ErrResumeFileMismatch)
	}
	candidate := &transfer.DownloadInstanceInfoExport{}
	candidate.SetInstanceInfo(eii)
	if err := validateCheckpoint(candidate); err != nil {
		return err
	}

	var (
		data []byte
		err  error
	)
	switch is.format {
	case InstanceStateStorageFormatProto3:
		data, err = proto.Marshal(candidate)
	default:
		data, err = jsoniter.Marshal(candidate)
	}
	if err != nil {
		return err
	}
	if int64(len(data)) > maxCheckpointSize {
		return fmt.Errorf("%w: checkpoint size %d exceeds limit", ErrResumeFileMismatch, len(data))
	}
	if is.ii == nil {
		is.ii = &transfer.DownloadInstanceInfoExport{}
	}
	is.ii.SetInstanceInfo(eii)

	// Verify this handle is writable without destroying the previous record.
	// Writing in place could leave a torn, but still parseable, range list.
	info, err := is.saveFile.Stat()
	if err != nil {
		return err
	}
	err = is.saveFile.Truncate(info.Size())
	if err != nil {
		return err
	}
	name := is.filename
	temp, err := os.CreateTemp(filepath.Dir(name), ".pcs-download-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	n, err := temp.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = temp.Sync()
	}
	err = errors.Join(err, temp.Close())
	if err != nil {
		return err
	}
	// Snapshot preparation and flushing can take time. Do not publish over a
	// pathname that stopped referring to our owned checkpoint in that interval.
	if err := is.verifyCheckpointLocked(); err != nil {
		return err
	}
	// Windows cannot replace an open destination on all supported filesystems.
	if err = is.saveFile.Close(); err != nil {
		return err
	}
	if err = os.Rename(temp.Name(), name); err != nil {
		reopened, openErr := os.OpenFile(name, os.O_RDWR, 0600)
		if openErr == nil {
			is.saveFile = reopened
		}
		return errors.Join(err, openErr)
	}
	is.saveFile, err = os.OpenFile(name, os.O_RDWR, 0600)
	return err
}

// Close 关闭
func (is *InstanceState) Close() error {
	is.mu.Lock()
	defer is.mu.Unlock()
	return is.closeCheckpointLocked()
}

func (der *Downloader) initInstanceState(format InstanceStateStorageFormat) (err error) {
	if der.instanceState != nil {
		return errors.New("already initInstanceState")
	}

	if der.config.IsTest || der.config.InstanceStatePath == "" {
		der.instanceState = NewInstanceState(nil, format)
		return nil
	}
	der.instanceState, err = openCheckpointState(der.config.InstanceStatePath, format)
	if err != nil {
		return err
	}
	return nil
}

func (der *Downloader) removeInstanceState() error {
	if !der.config.IsTest && der.config.InstanceStatePath != "" {
		return der.instanceState.removeCheckpoint()
	}
	return der.instanceState.Close()
}
