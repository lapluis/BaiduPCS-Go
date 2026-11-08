package pcscommand

import "testing"

func TestValidateShareTransferParamsAcceptsSupportedForms(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params []string
	}{
		{"query-code", []string{"https://pan.baidu.com/s/1fixture?pwd=abcd"}},
		{"explicit-code", []string{"https://pan.baidu.com/s/1fixture", "abcd"}},
		{"code-override", []string{"https://pan.baidu.com/s/1fixture?pwd=wrong", "abcd"}},
		{"share-text", []string{"分享链接：https://pan.baidu.com/s/1fixture?pwd=abcd 提取码：abcd"}},
		{"init-url", []string{"https://pan.baidu.com/share/init?surl=fixture&pwd=abcd"}},
		{"trimmed-http", []string{"  http://pan.baidu.com/s/1fixture  ", "abcd"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No account or client is available to this pure preflight check.
			if err := ValidateShareTransferParams(tc.params); err != nil {
				t.Fatalf("supported share input rejected: %v", err)
			}
		})
	}
}
