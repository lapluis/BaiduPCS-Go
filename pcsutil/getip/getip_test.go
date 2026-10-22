package getip

import (
	"os"
	"testing"
)

func TestGetIP(t *testing.T) {
	if os.Getenv("BAIDUPCS_EXTERNAL_TESTS") != "1" {
		t.Skip("external IP services: set BAIDUPCS_EXTERNAL_TESTS=1 to opt in")
	}
	ipAddr, err := IPInfo(false)
	if err != nil {
		t.Errorf("err: %s\n", err)
		return
	}

	t.Logf("from ipify: %s\n", ipAddr)

	ipAddr, err = IPInfoFromNetease()
	if err != nil {
		t.Errorf("err: %s\n", err)
		return
	}

	t.Logf("from netease: %s\n", ipAddr)
}
