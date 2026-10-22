package pcscommand

import (
	"strings"
	"testing"
)

// These cases fail before any network request, so a nil client is safe and the
// validation shared by the legacy command and pcsclone is covered.
func TestShareTransferValidation(t *testing.T) {
	cases := []struct {
		name   string
		params []string
		want   string
	}{
		{name: "empty", params: nil, want: "分享链接不能为空"},
		{name: "too many", params: []string{"a", "b", "c"}, want: "参数过多"},
		{name: "bad feature", params: []string{"https://pan.baidu.com/s/x", "abcd"}, want: "链接地址或提取码非法"},
		{name: "bad code", params: []string{"https://pan.baidu.com/s/1abcdefghijklmnopqrstuv", "abc"}, want: "链接地址或提取码非法"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ShareTransferTo(nil, tc.params, "/", nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
