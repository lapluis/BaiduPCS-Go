package jsonhelper

import (
	stdjson "encoding/json"
	"errors"
	"github.com/json-iterator/go"
	"io"
)

// UnmarshalData 将 r 中的 json 格式的数据, 解析到 data
func UnmarshalData(r io.Reader, data interface{}) error {
	d := stdjson.NewDecoder(r)
	if err := d.Decode(data); err != nil {
		return err
	}
	var extra interface{}
	if err := d.Decode(&extra); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("jsonhelper: unexpected trailing JSON value")
	}
	return nil
}

// MarshalData 将 data, 生成 json 格式的数据, 写入 w 中
func MarshalData(w io.Writer, data interface{}) error {
	e := jsoniter.NewEncoder(w)
	return e.Encode(data)
}
