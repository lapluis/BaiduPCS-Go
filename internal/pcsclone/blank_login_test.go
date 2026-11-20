package pcsclone

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBlankLoginCredentialsDoNotStartInteractiveLogin(t *testing.T) {
	// If validation regresses, give the legacy prompt EOF rather than allowing
	// a test to read the user's terminal. Its initialization requests are proxied
	// to the fixture server below, which never forwards them to Baidu.
	emptyInput, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	previousInput := os.Stdin
	os.Stdin = emptyInput
	defer func() { os.Stdin = previousInput; emptyInput.Close() }()
	for _, operation := range []string{"create", "reconnect"} {
		for _, flag := range []string{"bduss", "cookies", "ptoken", "stoken"} {
			for _, flags := range [][]string{{"--" + flag, ""}, {"--" + flag, " \t "}, {"--" + flag + "="}, {"--" + flag + "= \t"}} {
				t.Run(operation+"/"+strings.Join(flags, " "), func(t *testing.T) {
					var requests atomic.Int64
					name := "new"
					if operation == "reconnect" {
						name = "bd"
					}
					c, out := commandFixture(t, func(w http.ResponseWriter, _ *http.Request) {
						requests.Add(1)
						w.WriteHeader(http.StatusBadGateway)
					}, buildConfigGroup().Group, operation, []string{name}, flags)
					c.app.Config.BaiduUserList[0].BDUSS = "SYNTHETIC-OLD"
					if err := c.app.Config.Save(); err != nil {
						t.Fatal(err)
					}
					if err := c.app.Resolver.Save(); err != nil {
						t.Fatal(err)
					}
					read := func(name string) []byte {
						t.Helper()
						data, err := os.ReadFile(name)
						if err != nil {
							t.Fatal(err)
						}
						return data
					}
					aliasFile := filepath.Join(filepath.Dir(c.app.ConfigFile), "rc_remotes.json")
					before, aliases := read(c.app.ConfigFile), read(aliasFile)
					err := c.cmd.Run(c)
					var exit *exitError
					want := "require --bduss"
					if flag == "bduss" || flag == "cookies" {
						want = "missing value for --" + flag
					}
					if !errors.As(err, &exit) || exit.code != 2 || !strings.Contains(err.Error(), want) || out.Len() != 0 {
						t.Errorf("blank credential did not fail validation: err=%v output=%q", err, out.String())
					}
					if requests.Load() != 0 {
						t.Errorf("blank credential started %d login initialization requests", requests.Load())
					}
					if !bytes.Equal(before, read(c.app.ConfigFile)) || !bytes.Equal(aliases, read(aliasFile)) {
						t.Error("blank credential changed persisted configuration")
					}
				})
			}
		}
	}
}

func TestEmptyOptionalTokensRemainValidWithBDUSS(t *testing.T) {
	for _, flag := range []string{"ptoken", "stoken"} {
		for _, args := range [][]string{{"--" + flag, ""}, {"--" + flag + "="}} {
			_, flags, err := parseCommandFlags(append([]string{"--bduss", "SYNTHETIC"}, args...), credentialFlags())
			if err != nil {
				t.Fatal(err)
			}
			in, err := (&commandContext{flags: flags}).loginInput()
			if err != nil || in.bduss != "SYNTHETIC" || in.ptoken != "" || in.stoken != "" {
				t.Fatalf("optional empty token rejected: %s: %v", flag, err)
			}
		}
	}
}
