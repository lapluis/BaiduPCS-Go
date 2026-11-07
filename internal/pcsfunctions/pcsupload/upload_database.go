package pcsupload

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/filelock"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/jsonhelper"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type (
	// Uploading 未完成上传的信息
	Uploading struct {
		*checksum.LocalFileMeta
		State *uploader.InstanceState `json:"state"`
	}

	// UploadingDatabase 未完成上传的数据库
	UploadingDatabase struct {
		lock          sync.RWMutex
		UploadingList []*Uploading `json:"upload_state"`
		Timestamp     int64        `json:"timestamp"`

		filename string
		version  [sha256.Size]byte
		closed   bool
		openFile func(string, int, os.FileMode) (*os.File, error)
	}
)

// ErrCheckpointSave identifies upload progress persistence failures.
var ErrCheckpointSave = errors.New("upload resume checkpoint could not be saved")

// ErrCheckpointConflict requires reopening the database before saving again.
var ErrCheckpointConflict = errors.New("upload resume database changed; reopen the latest snapshot")

var statUploadSource = os.Stat

// NewUploadingDatabase 初始化未完成上传的数据库, 从库中读取内容
func NewUploadingDatabase() (ud *UploadingDatabase, err error) {
	return NewUploadingDatabaseAt(filepath.Join(pcsconfig.GetConfigDir(), UploadingFileName))
}

// NewUploadingDatabaseAt isolates resume state for independent transfer workers.
func NewUploadingDatabaseAt(filename string) (ud *UploadingDatabase, err error) {
	if filename == "" {
		return nil, errors.New("empty upload database path")
	}
	filename, err = filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	filename, err = filelock.CanonicalPath(filename)
	if err != nil {
		return nil, err
	}
	lease, err := filelock.TryLock(filename + ".lock")
	if err != nil {
		return nil, fmt.Errorf("lock upload resume database: %w", err)
	}
	defer func() {
		if closeErr := lease.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
			ud = nil
		}
	}()
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}

	ud = &UploadingDatabase{
		filename: filename, version: sha256.Sum256(nil), openFile: os.OpenFile,
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
			ud = nil
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	if info.Size() <= 0 {
		return ud, nil
	}

	loaded := ud
	digest := sha256.New()
	err = jsonhelper.UnmarshalData(io.TeeReader(file, digest), &loaded)
	if err == nil && loaded == nil {
		err = errors.New("upload resume database must be an object, not null")
	}
	if err != nil {
		return nil, fmt.Errorf("decode upload resume database: %w", err)
	}
	if err := validateUploadDatabaseEntries(ud.UploadingList); err != nil {
		return nil, err
	}
	copy(ud.version[:], digest.Sum(nil))
	for _, entry := range ud.UploadingList {
		if entry == nil || entry.State == nil {
			continue
		}
		for _, block := range entry.State.BlockList {
			if block.CheckSum != "" {
				valid := false
				if len(block.CheckSum) == 2*md5.Size {
					_, err := hex.DecodeString(block.CheckSum)
					valid = err == nil
				}
				if !valid {
					// Older versions cached any nonempty response digest. Only
					// reupload that block; keep the session and valid parts.
					block.CheckSum = ""
				}
			}
		}
	}

	return ud, nil
}

// Apply the same structural constraints before reading or replacing a record.
func validateUploadDatabaseEntries(entries []*Uploading) error {
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		if entry.State == nil {
			if entry.LocalFileMeta != nil && entry.BlocksList != nil {
				return errors.New("invalid upload resume database: block checksums without upload state")
			}
			continue
		}
		for _, block := range entry.State.BlockList {
			if block == nil {
				return errors.New("invalid upload resume database: null block")
			}
		}
	}
	return nil
}

// Save 保存内容
func (ud *UploadingDatabase) Save() (saveErr error) {
	defer func() {
		if saveErr != nil {
			saveErr = fmt.Errorf("%w: %w", ErrCheckpointSave, saveErr)
		}
	}()
	ud.lock.Lock()
	defer ud.lock.Unlock()
	if ud.closed || ud.filename == "" {
		return errors.New("upload resume database is closed")
	}
	if err := validateUploadDatabaseEntries(ud.UploadingList); err != nil {
		return err
	}
	name := ud.filename
	lease, err := filelock.TryLock(name + ".lock")
	if err != nil {
		return fmt.Errorf("lock upload resume database: %w", err)
	}
	defer func() { saveErr = errors.Join(saveErr, lease.Close()) }()
	currentInfo, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if !currentInfo.Mode().IsRegular() {
		return ErrCheckpointConflict
	}
	file, err := ud.openFile(name, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	info, checkErr := file.Stat()
	if checkErr == nil {
		digest := sha256.New()
		_, checkErr = io.Copy(digest, io.NewSectionReader(file, 0, info.Size()))
		var current [sha256.Size]byte
		copy(current[:], digest.Sum(nil))
		if checkErr == nil && current != ud.version {
			checkErr = ErrCheckpointConflict
		}
	}
	if checkErr == nil {
		checkErr = file.Truncate(info.Size())
	}
	checkErr = errors.Join(checkErr, file.Close())
	if checkErr != nil {
		return checkErr
	}

	ud.Timestamp = time.Now().Unix()

	var (
		builder    = &strings.Builder{}
		marshalErr = jsonhelper.MarshalData(builder, ud)
	)
	if marshalErr != nil {
		return marshalErr
	}
	temp, err := os.CreateTemp(filepath.Dir(name), ".pcs-upload-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	n, err := temp.WriteString(builder.String())
	if err == nil && n != builder.Len() {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = temp.Sync()
	}
	err = errors.Join(err, temp.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(temp.Name(), name); err != nil {
		return err
	}
	ud.version = sha256.Sum256([]byte(builder.String()))
	return nil
}

// UpdateUploading 更新正在上传
func (ud *UploadingDatabase) UpdateUploading(meta *checksum.LocalFileMeta, state *uploader.InstanceState) {
	ud.lock.Lock()
	defer ud.lock.Unlock()
	if meta == nil {
		return
	}
	meta = cloneUploadMeta(meta)
	state = cloneUploadState(state)
	meta.CompleteAbsPath()
	for k, uploading := range ud.UploadingList {
		if uploading == nil || uploading.LocalFileMeta == nil {
			continue
		}
		if uploading.LocalFileMeta.EqualLengthMD5(meta) || uploading.LocalFileMeta.Path == meta.Path {
			ud.UploadingList[k] = &Uploading{LocalFileMeta: meta, State: state}
			return
		}
	}

	ud.UploadingList = append(ud.UploadingList, &Uploading{
		LocalFileMeta: meta,
		State:         state,
	})
}

// UpdateFullBlock 一次性更新全部block的md5值
func (ud *UploadingDatabase) UpdateFullBlock(meta *checksum.LocalFileMeta, state *uploader.InstanceState) {
	ud.lock.Lock()
	defer ud.lock.Unlock()
	if meta == nil || state == nil || len(state.BlockList) < len(meta.BlocksList) {
		return
	}
	meta = cloneUploadMeta(meta)
	state = cloneUploadState(state)
	for i := range meta.BlocksList {
		if state.BlockList[i] == nil {
			return
		}
	}
	meta.CompleteAbsPath()
	for k, uploading := range ud.UploadingList {
		if uploading == nil || uploading.LocalFileMeta == nil {
			continue
		}
		if uploading.LocalFileMeta.EqualLengthMD5(meta) || uploading.LocalFileMeta.Path == meta.Path {
			if len(meta.BlocksList) > 0 {
				for index, md5 := range meta.BlocksList {
					state.BlockList[index].CheckSum = md5
				}
			}
			ud.UploadingList[k] = &Uploading{LocalFileMeta: meta, State: state}
			return
		}
	}
}

func (ud *UploadingDatabase) deleteIndex(k int) {
	ud.UploadingList = append(ud.UploadingList[:k], ud.UploadingList[k+1:]...)
}

// Delete 删除
func (ud *UploadingDatabase) Delete(meta *checksum.LocalFileMeta) bool {
	ud.lock.Lock()
	defer ud.lock.Unlock()
	if meta == nil {
		return false
	}
	meta.CompleteAbsPath()
	for k, uploading := range ud.UploadingList {
		if uploading == nil || uploading.LocalFileMeta == nil {
			continue
		}
		if uploading.LocalFileMeta.EqualLengthMD5(meta) || uploading.LocalFileMeta.Path == meta.Path {
			ud.deleteIndex(k)
			return true
		}
	}
	return false
}

// Search 搜索
func (ud *UploadingDatabase) Search(meta *checksum.LocalFileMeta) *uploader.InstanceState {
	state, _ := ud.SearchWithError(meta)
	return state
}

// SearchWithError distinguishes a cache miss from an unreadable source, so
// callers do not replace a recoverable upload session after a transient error.
func (ud *UploadingDatabase) SearchWithError(meta *checksum.LocalFileMeta) (*uploader.InstanceState, error) {
	if meta == nil {
		return nil, nil
	}

	meta.CompleteAbsPath()
	ud.lock.Lock()
	defer ud.lock.Unlock()
	for k, uploading := range ud.UploadingList {
		if uploading == nil || uploading.LocalFileMeta == nil {
			continue
		}
		if uploading.BlocksList == nil && (uploading.State == nil || uploading.State.Uploadid == "") {
			continue
		}
		if uploading.LocalFileMeta.MD5 != nil && uploading.LocalFileMeta.EqualLengthMD5(meta) {
			return cloneUploadState(uploading.State), nil
		}
		if uploading.LocalFileMeta.Path == meta.Path {
			verifiedCurrent := len(meta.MD5) == md5.Size
			verifiedStored := len(uploading.MD5) == md5.Size
			if meta.Length != uploading.LocalFileMeta.Length || (verifiedCurrent && verifiedStored && !uploading.LocalFileMeta.EqualLengthMD5(meta)) {
				ud.deleteIndex(k)
				return nil, nil
			}
			// Only inspect the candidate being requested. A lookup must not
			// discard another task's progress because its device is unavailable.
			if uploading.ModTime != -1 {
				info, err := statUploadSource(uploading.Path)
				if err != nil {
					if !errors.Is(err, os.ErrNotExist) {
						return nil, fmt.Errorf("inspect upload resume source: %w", err)
					}
					ud.deleteIndex(k)
					return nil, nil
				}
				if uploading.Length != info.Size() || uploading.ModTime != info.ModTime().Unix() || (uploading.ModTimeNS != 0 && uploading.ModTimeNS != info.ModTime().UnixNano()) {
					ud.deleteIndex(k)
					return nil, nil
				}
			}
			// Legacy second-only timestamps cannot exclude a same-second
			// rewrite. A verified matching MD5 already returned above.
			if uploading.ModTime != -1 && uploading.ModTimeNS == 0 {
				ud.deleteIndex(k)
				return nil, nil
			}

			// An already verified current digest takes precedence over cached
			// metadata, including an absent or incomplete cached digest.
			if !verifiedCurrent {
				cached := cloneUploadMeta(uploading.LocalFileMeta)
				meta.MD5 = cached.MD5
				meta.SliceMD5 = cached.SliceMD5
				meta.BlocksList = cached.BlocksList
			}
			return cloneUploadState(uploading.State), nil
		}
	}
	return nil, nil
}

// Close 关闭数据库
func (ud *UploadingDatabase) Close() error {
	ud.lock.Lock()
	defer ud.lock.Unlock()
	ud.closed = true
	return nil
}

// Stored and returned snapshots must not share mutable slices or blocks with
// upload tasks, which continue updating their own state outside ud.lock.
func cloneUploadMeta(meta *checksum.LocalFileMeta) *checksum.LocalFileMeta {
	copy := *meta
	if meta.MD5 != nil {
		copy.MD5 = append([]byte{}, meta.MD5...)
	}
	if meta.SliceMD5 != nil {
		copy.SliceMD5 = append([]byte{}, meta.SliceMD5...)
	}
	if meta.BlocksList != nil {
		copy.BlocksList = append([]string{}, meta.BlocksList...)
	}
	return &copy
}

func cloneUploadState(state *uploader.InstanceState) *uploader.InstanceState {
	if state == nil {
		return nil
	}
	copy := *state
	if state.BlockList != nil {
		copy.BlockList = make([]*uploader.BlockState, len(state.BlockList))
		for i, block := range state.BlockList {
			if block != nil {
				value := *block
				copy.BlockList[i] = &value
			}
		}
	}
	return &copy
}
