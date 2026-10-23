package pcsupload

import (
	"crypto/md5"
	"errors"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/converter"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/jsonhelper"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
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

		dataFile *os.File
	}
)

// NewUploadingDatabase 初始化未完成上传的数据库, 从库中读取内容
func NewUploadingDatabase() (ud *UploadingDatabase, err error) {
	return NewUploadingDatabaseAt(filepath.Join(pcsconfig.GetConfigDir(), UploadingFileName))
}

// NewUploadingDatabaseAt isolates resume state for independent transfer workers.
func NewUploadingDatabaseAt(filename string) (ud *UploadingDatabase, err error) {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}

	ud = &UploadingDatabase{
		dataFile: file,
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}

	if info.Size() <= 0 {
		return ud, nil
	}

	loaded := ud
	err = jsonhelper.UnmarshalData(file, &loaded)
	if err == nil && loaded == nil {
		err = errors.New("upload resume database must be an object, not null")
	}
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("decode upload resume database: %w", err)
	}
	for _, entry := range ud.UploadingList {
		if entry == nil {
			continue
		}
		if entry.State == nil {
			if entry.LocalFileMeta != nil && entry.BlocksList != nil {
				file.Close()
				return nil, errors.New("invalid upload resume database: block checksums without upload state")
			}
			continue
		}
		for _, block := range entry.State.BlockList {
			if block == nil {
				file.Close()
				return nil, errors.New("invalid upload resume database: null block")
			}
		}
	}

	return ud, nil
}

// Save 保存内容
func (ud *UploadingDatabase) Save() error {
	ud.lock.Lock()
	defer ud.lock.Unlock()
	if ud.dataFile == nil {
		return errors.New("dataFile is nil")
	}

	ud.Timestamp = time.Now().Unix()

	var (
		builder = &strings.Builder{}
		err     = jsonhelper.MarshalData(builder, ud)
	)
	if err != nil {
		panic(err)
	}

	err = ud.dataFile.Truncate(int64(builder.Len()))
	if err != nil {
		return err
	}

	str := builder.String()
	_, err = ud.dataFile.WriteAt(converter.ToBytes(str), 0)
	if err != nil {
		return err
	}

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
	if meta == nil {
		return nil
	}

	meta.CompleteAbsPath()
	ud.lock.Lock()
	defer ud.lock.Unlock()
	ud.clearModTimeChangeLocked()
	for k, uploading := range ud.UploadingList {
		if uploading == nil || uploading.LocalFileMeta == nil {
			continue
		}
		if uploading.BlocksList == nil && (uploading.State == nil || uploading.State.Uploadid == "") {
			continue
		}
		if uploading.LocalFileMeta.MD5 != nil && uploading.LocalFileMeta.EqualLengthMD5(meta) {
			return cloneUploadState(uploading.State)
		}
		if uploading.LocalFileMeta.Path == meta.Path {
			verifiedCurrent := len(meta.MD5) == md5.Size
			verifiedStored := len(uploading.MD5) == md5.Size
			if meta.Length != uploading.LocalFileMeta.Length || (verifiedCurrent && verifiedStored && !uploading.LocalFileMeta.EqualLengthMD5(meta)) {
				ud.deleteIndex(k)
				return nil
			}

			// An already verified current digest takes precedence over cached
			// metadata, including an absent or incomplete cached digest.
			if !verifiedCurrent {
				cached := cloneUploadMeta(uploading.LocalFileMeta)
				meta.MD5 = cached.MD5
				meta.SliceMD5 = cached.SliceMD5
				meta.BlocksList = cached.BlocksList
			}
			return cloneUploadState(uploading.State)
		}
	}
	return nil
}

// Caller holds ud.lock throughout cleanup and lookup.
func (ud *UploadingDatabase) clearModTimeChangeLocked() {
	for i := 0; i < len(ud.UploadingList); i++ {
		uploading := ud.UploadingList[i]
		if uploading == nil || uploading.LocalFileMeta == nil {
			continue
		}

		if uploading.ModTime == -1 { // 忽略
			continue
		}

		info, err := os.Stat(uploading.LocalFileMeta.Path)
		if err != nil {
			ud.deleteIndex(i)
			i--
			pcsUploadVerbose.Warnf("clear invalid file path: %s, err: %s\n", uploading.LocalFileMeta.Path, err)
			continue
		}

		if uploading.LocalFileMeta.ModTime != info.ModTime().Unix() {
			ud.deleteIndex(i)
			i--
			pcsUploadVerbose.Infof("clear modified file path: %s\n", uploading.LocalFileMeta.Path)
			continue
		}
	}
}

// Close 关闭数据库
func (ud *UploadingDatabase) Close() error {
	ud.lock.Lock()
	defer ud.lock.Unlock()
	if ud.dataFile == nil {
		return nil
	}
	err := ud.dataFile.Close()
	ud.dataFile = nil
	return err
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
