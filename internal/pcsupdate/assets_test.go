package pcsupdate

import "testing"

func TestUpdateAssetMatchesWholeLiteralName(t *testing.T) {
	re := updateAssetRegexp("v4.0.2", "windows", "amd64")
	for _, name := range []string{"BaiduPCS-Go-v4.0.2-windows-amd64.zip", "BaiduPCS-Go-v4.0.2-windows-x86_64.zip", "BaiduPCS-Go-v4.0.2-windows-x64.zip"} {
		if !re.MatchString(name) {
			t.Errorf("valid asset rejected: %s", name)
		}
	}
	for _, name := range []string{"prefix-BaiduPCS-Go-v4.0.2-windows-amd64.zip", "BaiduPCS-Go-v4x0x2-windows-amd64.zip", "BaiduPCS-Go-v4.0.2-windows-amd64.zip.sig", "BaiduPCS-Go-v4.0.2-linux-amd64.zip", "BaiduPCS-Go-v4.0.2-windows-arm64.zip"} {
		if re.MatchString(name) {
			t.Errorf("incorrect update asset accepted: %s", name)
		}
	}
	if !updateAssetRegexp("v4.0.2", "linux", "mipsle").MatchString("BaiduPCS-Go-v4.0.2-linux-mipsel.zip") {
		t.Error("architecture alias lost")
	}
}
