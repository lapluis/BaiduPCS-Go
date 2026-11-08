package pcsclone

import (
	"bytes"
	"strings"
	"testing"
)

func TestUnknownOptionsDoNotEchoAttachedCredentialValues(t *testing.T) {
	const secret = "SYNTHETIC-DO-NOT-ECHO=VALUE"
	for _, credential := range []string{"bduss", "ptoken", "stoken", "cookies", "username", "password"} {
		for _, placement := range []string{"global-long", "global-short", "command-short", "command-typo"} {
			t.Run(credential+"/"+placement, func(t *testing.T) {
				option := "--" + credential
				var args []string
				switch placement {
				case "global-long":
					args = []string{option + "=" + secret, "config", "create", "aa"}
				case "global-short":
					option = "-" + credential
					args = []string{option + "=" + secret, "config", "create", "aa"}
				case "command-short":
					option = "-" + credential
					args = []string{"config", "create", "aa", option + "=" + secret}
				case "command-typo":
					option += "-typo"
					args = []string{"config", "create", "aa", option + "=" + secret}
				}
				var out, diagnostics bytes.Buffer
				app := &App{CWD: t.TempDir(), Stdout: &out, Stderr: &diagnostics}
				code := app.run(args)
				if code != 2 || app.initialized || out.Len() != 0 || !strings.Contains(diagnostics.String(), "unknown") || !strings.Contains(diagnostics.String(), option) {
					t.Errorf("option rejection changed: code=%d initialized=%t out=%q stderr=%q", code, app.initialized, out.String(), diagnostics.String())
				}
				if strings.Contains(diagnostics.String(), secret) || strings.Contains(diagnostics.String(), "SYNTHETIC-DO-NOT-ECHO") {
					t.Errorf("attached credential value was echoed: %q", diagnostics.String())
				}
			})
		}
	}
}

func TestRecognizedOptionsKeepEqualsInValues(t *testing.T) {
	const value = "synthetic=value=with=equals"
	for _, name := range []string{"bduss", "ptoken", "stoken", "cookies", "username", "password"} {
		_, flags, err := parseCommandFlags([]string{"--" + name + "=" + value}, credentialFlags())
		if err != nil || flags.String(name) != value {
			t.Fatalf("credential parsing changed for %s: flags=%v err=%v", name, flags, err)
		}
	}
	g, err := parseGlobalOptions([]string{"--config=directory/settings=one.json", "version"})
	if err != nil || g.config != "directory/settings=one.json" {
		t.Fatalf("config path changed: %+v err=%v", g, err)
	}
	paths, _, err := parseCommandFlags([]string{"--", "--password=" + value}, credentialFlags())
	if err != nil || len(paths) != 1 || paths[0] != "--password="+value {
		t.Fatalf("literal positional argument changed: %q err=%v", paths, err)
	}
}
