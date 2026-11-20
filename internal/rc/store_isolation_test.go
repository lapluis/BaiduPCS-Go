package rc

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func TestResolverAndCLIWithInjectedStoreAreIsolatedFromGlobalConfig(t *testing.T) {
	globalBefore := pcsconfig.Config
	globalDirBefore := pcsconfig.GetConfigDir()

	dirA := filepath.Join(t.TempDir(), "cfg-a")
	dirB := filepath.Join(t.TempDir(), "cfg-b")
	if err := os.MkdirAll(dirA, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0700); err != nil {
		t.Fatal(err)
	}

	cfgA := pcsconfig.NewConfig(filepath.Join(dirA, "pcs_config.json"))
	cfgA.AppID = 1001
	cfgA.EnableHTTPS = false
	cfgA.BaiduUserList = pcsconfig.BaiduUserList{
		&pcsconfig.Baidu{BaiduBase: pcsconfig.BaiduBase{UID: 101, Name: "alice"}, BDUSS: "SYNTHETIC-A"},
	}

	cfgB := pcsconfig.NewConfig(filepath.Join(dirB, "pcs_config.json"))
	cfgB.AppID = 2002
	cfgB.EnableHTTPS = true
	cfgB.BaiduUserList = pcsconfig.BaiduUserList{
		&pcsconfig.Baidu{BaiduBase: pcsconfig.BaiduBase{UID: 202, Name: "bob"}, BDUSS: "SYNTHETIC-B"},
	}

	cwd := t.TempDir()

	var stdoutA, stderrA bytes.Buffer
	if code := runWithConfigIO([]string{"remote", "add", "alpha", "--uid", "101"}, cwd, cfgA, &stdoutA, &stderrA); code != 0 {
		t.Fatalf("remote add alpha failed: code=%d stderr=%q", code, stderrA.String())
	}
	var stdoutB, stderrB bytes.Buffer
	if code := runWithConfigIO([]string{"remote", "add", "beta", "--uid", "202"}, cwd, cfgB, &stdoutB, &stderrB); code != 0 {
		t.Fatalf("remote add beta failed: code=%d stderr=%q", code, stderrB.String())
	}
	// Adding an account that only exists in cfgB must fail against cfgA.
	var stdoutCross, stderrCross bytes.Buffer
	if code := runWithConfigIO([]string{"remote", "add", "wrong", "--uid", "202"}, cwd, cfgA, &stdoutCross, &stderrCross); code == 0 {
		t.Fatalf("expected cross-config remote add to fail, stdout=%q", stdoutCross.String())
	}

	resA, err := NewResolverWithStore(cwd, NewPCSConfigStore(cfgA, dirA))
	if err != nil {
		t.Fatal(err)
	}
	resB, err := NewResolverWithStore(cwd, NewPCSConfigStore(cfgB, dirB))
	if err != nil {
		t.Fatal(err)
	}

	epA, err := resA.Resolve("alpha:/docs")
	if err != nil {
		t.Fatalf("resolve alpha: %v", err)
	}
	remA, ok := epA.FS.(*Remote)
	if !ok || remA.UID != 101 || remA.PCS == nil {
		t.Fatalf("unexpected alpha endpoint: %#v", epA)
	}
	epB, err := resB.Resolve("beta:/photos")
	if err != nil {
		t.Fatalf("resolve beta: %v", err)
	}
	remB, ok := epB.FS.(*Remote)
	if !ok || remB.UID != 202 || remB.PCS == nil {
		t.Fatalf("unexpected beta endpoint: %#v", epB)
	}
	if _, err := resA.Resolve("beta:/photos"); err == nil {
		t.Fatal("cfgA resolver unexpectedly saw cfgB alias")
	}

	var listOut, listErr bytes.Buffer
	if code := runWithConfigIO([]string{"listremotes"}, cwd, cfgA, &listOut, &listErr); code != 0 || strings.TrimSpace(listOut.String()) != "alpha:" {
		t.Fatalf("listremotes cfgA: code=%d out=%q err=%q", code, listOut.String(), listErr.String())
	}

	if pcsconfig.Config != globalBefore || pcsconfig.GetConfigDir() != globalDirBefore {
		t.Fatalf("global pcsconfig state mutated: configSame=%v dir=%q wantDir=%q", pcsconfig.Config == globalBefore, pcsconfig.GetConfigDir(), globalDirBefore)
	}
}
