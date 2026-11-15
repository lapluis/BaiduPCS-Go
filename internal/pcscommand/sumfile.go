package pcscommand

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/qjfoidnh/BaiduPCS-Go/pcstable"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
)

// RunSumFileWithError prints available local checksums and reports all failures.
func RunSumFileWithError(paths []string, commandName string) error {
	if len(paths) == 0 {
		return errors.New("没有任何本地文件路径")
	}
	var failures []error
	for k, filePath := range paths {
		lp, err := checksum.GetFileSum(filePath, checksum.CHECKSUM_MD5|checksum.CHECKSUM_SLICE_MD5|checksum.CHECKSUM_CRC32)
		if err != nil {
			failures = append(failures, fmt.Errorf("[%d] %s: %w", k+1, filePath, err))
			continue
		}
		fmt.Printf("[%d] - [%s]:\n", k+1, filePath)
		strLength, strMd5, strSliceMd5, strCrc32 := strconv.FormatInt(lp.Length, 10), hex.EncodeToString(lp.MD5), hex.EncodeToString(lp.SliceMD5), strconv.FormatUint(uint64(lp.CRC32), 10)
		fileName := filepath.Base(filePath)
		regFileName := strings.ReplaceAll(strings.ReplaceAll(fileName, " ", "_"), "#", "_")
		tb := pcstable.NewTable(os.Stdout)
		tb.SetColumnAlignment([]int{tablewriter.ALIGN_LEFT, tablewriter.ALIGN_LEFT})
		tb.AppendBulk([][]string{
			{"文件大小", strLength}, {"md5", strMd5}, {"前256KB切片的md5", strSliceMd5}, {"crc32", strCrc32},
			{"秒传命令", commandName + " rapidupload -length=" + strLength + " -md5=" + strMd5 + " -slicemd5=" + strSliceMd5 + " -crc32=" + strCrc32 + " " + fileName},
			{"通用秒传链接", strMd5 + "#" + strSliceMd5 + "#" + strLength + "#" + regFileName},
		})
		tb.Render()
		fmt.Println()
	}
	return errors.Join(failures...)
}
