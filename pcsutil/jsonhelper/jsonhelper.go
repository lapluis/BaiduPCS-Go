package jsonhelper

import (
	"bufio"
	stdjson "encoding/json"
	"errors"
	"github.com/json-iterator/go"
	"io"
)

// UnmarshalObjectData decodes an API response that must be a JSON object.
// Check the opening token without buffering the entire response body.
func UnmarshalObjectData(r io.Reader, data interface{}) error {
	buffered := bufio.NewReader(r)
	for {
		first, err := buffered.Peek(1)
		if err != nil {
			return err
		}
		switch first[0] {
		case ' ', '\t', '\r', '\n':
			buffered.ReadByte()
			continue
		case '{':
			return UnmarshalData(buffered, data)
		default:
			return errors.New("jsonhelper: expected a JSON object response")
		}
	}
}

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
