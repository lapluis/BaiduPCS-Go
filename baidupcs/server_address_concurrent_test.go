package baidupcs

import (
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPCSAddressSnapshotsRemainWholeDuringUpdates(t *testing.T) {
	previous := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(previous)
	shortName := "short.fixture.invalid"
	longName := strings.Repeat("long", 15) + ".fixture.invalid"
	// Both strings have ample synthetic backing storage, so a pre-fix torn
	// pointer/length pair cannot read unrelated memory during this regression.
	shortBacking := shortName + strings.Repeat("a", 256-len(shortName))
	longBacking := longName + strings.Repeat("b", 256-len(longName))
	short, long := shortBacking[:len(shortName)], longBacking[:len(longName)]
	for _, kind := range []string{"getter", "url", "copy"} {
		t.Run(kind, func(t *testing.T) {
			pcs := NewPCS(1, "")
			pcs.lazyInit() // Configuration is immutable before upload workers start.
			pcs.SetPCSAddr(short)
			read := pcs.GetPCSAddr
			if kind == "url" {
				read = func() string { return pcs.URL().Host }
			}
			if kind == "copy" {
				read = func() string { return pcs.CopyPCS().GetPCSAddr() }
			}
			start := make(chan struct{})
			var joined sync.WaitGroup
			var bad atomic.Bool
			for worker := 0; worker < 2; worker++ {
				joined.Add(1)
				go func() {
					defer joined.Done()
					<-start
					for i := 0; i < 200000 && !bad.Load(); i++ {
						pcs.SetPCSAddr(short)
						pcs.SetPCSAddr(long)
					}
				}()
			}
			for worker := 0; worker < 4; worker++ {
				joined.Add(1)
				go func() {
					defer joined.Done()
					<-start
					for i := 0; i < 100000 && !bad.Load(); i++ {
						value := read()
						if value != short && value != long {
							bad.Store(true)
							return
						}
					}
				}()
			}
			close(start)
			joined.Wait()
			if bad.Load() {
				t.Fatal("concurrent update returned a mixed server address snapshot")
			}
		})
	}
}

func TestPCSAddressCopyIsIndependentAndEmptyUpdateIsIgnored(t *testing.T) {
	pcs := NewPCS(1, "")
	if pcs.GetPCSAddr() != "" || pcs.URL().Host != PCSBaiduCom {
		t.Fatal("default server changed")
	}
	pcs.SetPCSAddr("source.fixture.invalid")
	copy := pcs.CopyPCS()
	pcs.SetPCSAddr("next.fixture.invalid")
	if copy.GetPCSAddr() != "source.fixture.invalid" {
		t.Fatal("copy did not retain its address snapshot")
	}
	copy.SetPCSAddr("copy.fixture.invalid")
	pcs.SetPCSAddr("")
	if pcs.GetPCSAddr() != "next.fixture.invalid" || copy.URL().Host != "copy.fixture.invalid" {
		t.Fatal("address state leaked across copies or empty update cleared source")
	}
}
