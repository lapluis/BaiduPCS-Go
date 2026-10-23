package pcsconfig

import (
	"strings"
	"testing"
)

func TestSparseAccountList(t *testing.T) {
	c := NewConfig("")
	c.BaiduUserList = BaiduUserList{nil, &Baidu{BaiduBase: BaiduBase{UID: 7, Name: "fixture-user"}}, nil}
	if c.NumLogins() != 1 {
		t.Error("null entries counted as accounts")
	}
	defer func() {
		if recover() != nil {
			t.Fatal("account listing panicked on null entry")
		}
	}()
	if !strings.Contains(c.BaiduUserList.String(), "fixture-user") {
		t.Fatal("valid account omitted")
	}
	c.BaiduUserList = BaiduUserList{nil}
	if c.NumLogins() != 0 {
		t.Error("null-only list counted as logged in")
	}
}
