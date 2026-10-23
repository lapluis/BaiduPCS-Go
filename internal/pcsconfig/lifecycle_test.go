package pcsconfig

import (
	"github.com/urfave/cli"
	"os"
	"testing"
)

func TestDeleteActiveAccountClearsClient(t *testing.T) {
	for _, remaining := range []BaiduUserList{nil, {nil}, {nil, &Baidu{BaiduBase: BaiduBase{UID: 8}, BDUSS: "SYNTHETIC_NEXT"}}} {
		c := reloadFixture(t)
		c.InitDefaultConfig()
		user := &Baidu{BaiduBase: BaiduBase{UID: 7}, BDUSS: "SYNTHETIC_OLD"}
		c.BaiduUserList = append(BaiduUserList{user}, remaining...)
		c.setupNewUser(user)
		oldClient := c.ActiveUserBaiduPCS()
		if _, err := c.DeleteUser(&user.BaiduBase); err != nil {
			t.Fatal(err)
		}
		if c.ActiveUser().UID == 7 || c.ActiveUserBaiduPCS() == oldClient || c.ActiveUserBaiduPCS().GetBDUSS() == "SYNTHETIC_OLD" {
			t.Fatal("deleted account still active")
		}
		if len(remaining) == 2 && (c.BaiduActiveUID != 8 || c.ActiveUserBaiduPCS().GetBDUSS() != "SYNTHETIC_NEXT") {
			t.Fatal("did not select remaining valid account")
		}
	}
}

func TestFailedReloadCannotOverwriteConfig(t *testing.T) {
	c := reloadFixture(t)
	writeReloadFixture(t, c, `{}`)
	if err := c.Init(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`{"appid":1,`, `{"baidu_active_uid":7}`} {
		writeReloadFixture(t, c, invalid)
		ran, saved := false, false
		app := cli.NewApp()
		app.Commands = []cli.Command{{
			Name:   "mutate",
			Before: func(*cli.Context) error { return c.Reload() },
			Action: func(*cli.Context) error { ran = true; return nil },
			After:  func(*cli.Context) error { saved = true; return c.Save() },
		}}
		if err := app.Run([]string{"fixture", "mutate"}); err == nil {
			t.Fatal("expected load error")
		}
		if ran || !saved {
			t.Fatal("unexpected CLI hook execution")
		}
		if err := c.Save(); err == nil {
			t.Fatal("saved partial configuration after load failure")
		}
		data, err := os.ReadFile(c.FilePath())
		if err != nil || string(data) != invalid {
			t.Fatal("failed reload changed disk contents")
		}
	}
	writeReloadFixture(t, c, `{}`)
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal("successful reload did not restore saving:", err)
	}
}
