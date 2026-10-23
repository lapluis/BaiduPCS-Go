package pcsconfig

import (
	"strconv"
	"testing"
)

func TestRateUnits(t *testing.T) {
	for _, upload := range []bool{false, true} {
		c := NewConfig("")
		set := c.SetMaxDownloadRateByStr
		get := func() int64 { return c.MaxDownloadRate }
		if upload {
			set = c.SetMaxUploadRateByStr
			get = func() int64 { return c.MaxUploadRate }
		}
		for _, value := range []string{"2MB", "2MB/s", "2mb/S"} {
			if err := set(value); err != nil || get() != 2*1024*1024 {
				t.Fatalf("valid rate %q failed: %v", value, err)
			}
		}
		for _, value := range []string{"3MB/hour", "3MB/", "3MB/garbage", "3MB/s/s"} {
			if err := set(value); err == nil {
				t.Errorf("invalid rate %q accepted", value)
			}
			if get() != 2*1024*1024 {
				t.Error("invalid rate changed configuration")
			}
		}
	}
}

func TestCacheSizeNativeIntRange(t *testing.T) {
	c := NewConfig("")
	maximum := uint64(^uint(0) >> 1)
	if err := c.SetCacheSizeByStr(strconv.FormatUint(maximum, 10)); err != nil || uint64(c.CacheSize) != maximum {
		t.Fatal("maximum int cache size rejected", err)
	}
	if err := c.SetCacheSizeByStr(strconv.FormatUint(maximum+1, 10)); err == nil {
		t.Fatal("overflowing cache size accepted")
	}
	if uint64(c.CacheSize) != maximum {
		t.Fatal("overflow changed cache size")
	}
}
