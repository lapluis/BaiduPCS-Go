package netdisksign_test

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/netdisksign"
)

func TestSign2(t *testing.T) {
	want, err := base64.StdEncoding.DecodeString("8RxCbsVeSzn2UjxJAAiV9QQs/WetOj2FJUGwjsMG6SgxFMWlLS/U1Q==")
	if err != nil {
		t.Fatal(err)
	}
	got := netdisksign.Sign2([]rune("e8c7d729eea7b54551aa594f942decbe"), []rune("37dbe07ade9359c1aa70807e847f768c13360ad2"))
	if !bytes.Equal(got, want) {
		t.Fatalf("signature mismatch: got=%x want=%x", got, want)
	}
}
