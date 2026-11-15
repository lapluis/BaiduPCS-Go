package pcsconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestSwitchUserAndSavePublishesOrRestoresAccount(t *testing.T) {
	for _, initial := range []string{"active", "none"} {
		for _, state := range []string{"success", "missing", "nil", "save-failure", "load-failure"} {
			t.Run(initial+"/"+state, func(t *testing.T) {
				c := reloadFixture(t)
				c.InitDefaultConfig()
				old := &Baidu{BaiduBase: BaiduBase{UID: 7, Name: "Original"}, Workdir: "/old"}
				target := &Baidu{BaiduBase: BaiduBase{UID: 8, Name: "Target"}, Workdir: "/target"}
				c.BaiduUserList = BaiduUserList{old, target}
				if initial == "active" {
					if _, err := c.SwitchUser(&old.BaiduBase); err != nil {
						t.Fatal(err)
					}
				}
				if err := c.Save(); err != nil {
					t.Fatal(err)
				}
				original, err := os.ReadFile(c.FilePath())
				if err != nil {
					t.Fatal(err)
				}
				previousUID, previousUser, previousPCS := c.BaiduActiveUID, c.activeUser, c.pcs
				previousReplace := replaceConfigFile
				defer func() { replaceConfigFile = previousReplace }()
				saves := 0
				failure := errors.New("synthetic config publication failure")
				replaceConfigFile = func(a, b string) error {
					saves++
					if state == "save-failure" {
						return failure
					}
					return previousReplace(a, b)
				}
				selection := &target.BaiduBase
				wantErr := error(nil)
				switch state {
				case "missing":
					selection = &BaiduBase{UID: 99}
					wantErr = ErrBaiduUserNotFound
				case "nil":
					selection = nil
					wantErr = ErrBaiduUserNotFound
				case "save-failure":
					wantErr = failure
				case "load-failure":
					c.loadErr = failure
					wantErr = failure
				}
				selected, err := c.SwitchUserAndSave(selection)
				data, readErr := os.ReadFile(c.FilePath())
				if readErr != nil {
					t.Fatal(readErr)
				}
				if wantErr != nil {
					if !errors.Is(err, wantErr) || selected != nil {
						t.Fatalf("wrong failure: user=%v err=%v", selected, err)
					}
					if c.BaiduActiveUID != previousUID || c.activeUser != previousUser || c.pcs != previousPCS || !bytes.Equal(data, original) {
						t.Fatal("failure changed active account, client or stored config")
					}
					if state != "save-failure" && saves != 0 {
						t.Fatal("invalid selection or failed load attempted publication")
					}
					return
				}
				if err != nil || selected != target || c.activeUser != target || c.BaiduActiveUID != 8 || c.pcs == nil || saves != 1 {
					t.Fatalf("success not published: user=%v saves=%d err=%v", selected, saves, err)
				}
				var saved struct {
					UID uint64 `json:"baidu_active_uid"`
				}
				if err := json.Unmarshal(data, &saved); err != nil || saved.UID != 8 {
					t.Fatalf("saved selection: uid=%d err=%v", saved.UID, err)
				}
				if old.Workdir != "/old" || target.Workdir != "/target" {
					t.Fatal("switch changed per-user working directories")
				}
			})
		}
	}
}
