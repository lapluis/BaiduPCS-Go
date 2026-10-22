package baidupcs

import "testing"

func TestCopyPCSSharesCachePointer(t *testing.T) {
	pcs := NewPCS(1, "")
	copy := pcs.CopyPCS()
	if pcs.cacheOpMap == nil || pcs.cacheOpMap != copy.cacheOpMap {
		t.Fatal("copied client must share cache without copying locks")
	}
}
