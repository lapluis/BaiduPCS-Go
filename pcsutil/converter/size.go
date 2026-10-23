package converter

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const (
	// B byte
	B = (int64)(1 << (10 * iota))
	// KB kilobyte
	KB
	// MB megabyte
	MB
	// GB gigabyte
	GB
	// TB terabyte
	TB
	// PB petabyte
	PB
)

// ConvertFileSize 文件大小格式化输出
func ConvertFileSize(size int64, precision ...int) string {
	pint := "6"
	if len(precision) == 1 {
		pint = fmt.Sprint(precision[0])
	}
	if size < 0 {
		return "0B"
	}
	if size < KB {
		return fmt.Sprintf("%dB", size)
	}
	if size < MB {
		return fmt.Sprintf("%."+pint+"fKB", float64(size)/float64(KB))
	}
	if size < GB {
		return fmt.Sprintf("%."+pint+"fMB", float64(size)/float64(MB))
	}
	if size < TB {
		return fmt.Sprintf("%."+pint+"fGB", float64(size)/float64(GB))
	}
	if size < PB {
		return fmt.Sprintf("%."+pint+"fTB", float64(size)/float64(TB))
	}
	return fmt.Sprintf("%."+pint+"fPB", float64(size)/float64(PB))
}

// ParseFileSizeStr 将文件大小字符串转换成字节数
func ParseFileSizeStr(ss string) (size int64, err error) {
	if ss == "" {
		err = errors.New("converter: size is empty")
		return
	}
	if !(ss[0] == '.' || '0' <= ss[0] && ss[0] <= '9') {
		err = errors.New("converter: invalid size: " + ss)
		return
	}

	var i int
	for i < len(ss) && (ss[i] == '.' || ('0' <= ss[i] && ss[i] <= '9')) {
		i++
	}
	sizeStr, unitStr := ss[:i], ss[i:]
	amount, ok := new(big.Rat).SetString(sizeStr)
	if !ok {
		return 0, errors.New("converter: invalid size: " + ss)
	}
	var unit int64
	switch strings.ToUpper(unitStr) {
	case "", "B":
		unit = B
	case "K", "KB":
		unit = KB
	case "M", "MB":
		unit = MB
	case "G", "GB":
		unit = GB
	case "T", "TB":
		unit = TB
	case "P", "PB":
		unit = PB
	default:
		return 0, errors.New("converter: invalid unit " + unitStr)
	}
	// Exact decimal arithmetic avoids losing bytes above float64's integer
	// precision, and allows range checks before converting to int64.
	amount.Mul(amount, new(big.Rat).SetInt64(unit))
	if amount.Cmp(new(big.Rat).SetInt64(1<<63-1)) > 0 {
		return 0, errors.New("converter: size exceeds int64 range")
	}
	whole := new(big.Int).Quo(amount.Num(), amount.Denom())
	return whole.Int64(), nil
}
