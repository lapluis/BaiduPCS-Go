package pcsclone

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcscommand"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

var aliasPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]+$`)

func buildConfigGroup() *command {
	return &command{
		Name: "config", Category: "Account configuration",
		Summary: "Manage pcsclone accounts and remote names",
		Usage:   "pcsclone config <subcommand> [options]",
		Description: `Manage the independent pcsclone configuration. Credentials are
never printed. The legacy BaiduPCS-Go configuration is read only by the explicit
'config import-pcsgo' command.`,
		Group: []*command{
			{
				Name: "create", Summary: "Log in and create a remote name",
				Usage: "pcsclone config create NAME [options]",
				Description: `Log in to a Baidu account and store it under NAME. Without
credential options an interactive login is started. The new account is kept in
the pcsclone configuration directory only.`,
				MinArgs: 1, MaxArgs: 1, Flags: credentialFlags(), Run: runConfigCreate,
			},
			{
				Name: "reconnect", Summary: "Refresh credentials for a remote",
				Usage: "pcsclone config reconnect NAME [options]",
				Description: `Log in again for the account behind NAME and replace its
stored credentials. The returned account must be the same UID.`,
				MinArgs: 1, MaxArgs: 1, Flags: credentialFlags(), Run: runConfigReconnect,
			},
			{
				Name: "rename", Summary: "Rename a remote",
				Usage:   "pcsclone config rename OLD NEW",
				MinArgs: 2, MaxArgs: 2, Run: runConfigRename,
			},
			{
				Name: "delete", Summary: "Delete a remote and its unused account",
				Usage: "pcsclone config delete NAME [--keep-account]",
				Description: `Remove NAME. If no other remote refers to the same UID, the
stored account credentials are removed too unless --keep-account is used.
Netdisk files are never touched.`,
				MinArgs: 1, MaxArgs: 1,
				Flags: []flagSpec{boolFlag("keep-account", "", "Keep account credentials when no alias remains")},
				Run:   runConfigDelete,
			},
			{
				Name: "show", Summary: "Show remote names without secrets",
				Usage: "pcsclone config show [NAME] [--json]",
				Description: `Show UID, username and whether credentials are present.
Credential values are always redacted.`,
				MinArgs: 0, MaxArgs: 1,
				Flags: []flagSpec{boolFlag("json", "", "Write a JSON array")},
				Run:   runConfigShow,
			},
			{
				Name: "file", Summary: "Print configuration file locations",
				Usage:   "pcsclone config file [--json]",
				MinArgs: 0, MaxArgs: 0,
				Flags: []flagSpec{boolFlag("json", "", "Write a JSON object")},
				Run:   runConfigFile,
			},
			{
				Name: "import-pcsgo", Summary: "Import accounts from BaiduPCS-Go",
				Usage: "pcsclone config import-pcsgo [options]",
				Description: `Copy accounts and remote aliases from a legacy BaiduPCS-Go
configuration into pcsclone's own configuration. The legacy file is only read;
it is not modified.`,
				MinArgs: 0, MaxArgs: 0,
				Flags: []flagSpec{
					stringFlag("from", "", "PATH", "Legacy config file or directory"),
					boolFlag("dry-run", "n", "Report without importing accounts or aliases"),
					boolFlag("json", "", "Write a JSON summary"),
				},
				Run: runConfigImport,
			},
		},
	}
}

func credentialFlags() []flagSpec {
	return []flagSpec{
		stringFlag("bduss", "", "BDUSS", "Log in with a BDUSS value"),
		stringFlag("ptoken", "", "PTOKEN", "PTOKEN paired with --bduss (optional)"),
		stringFlag("stoken", "", "STOKEN", "STOKEN paired with --bduss (optional)"),
		stringFlag("cookies", "", "COOKIES", "Log in with a Baidu cookie string containing BDUSS"),
		stringFlag("username", "", "USER", "Username for an interactive login"),
		stringFlag("password", "", "PASSWORD", "Password for a non-prompting login"),
	}
}

type loginInput struct {
	bduss    string
	ptoken   string
	stoken   string
	cookies  string
	username string
	password string
}

var cookieBDUSS = regexp.MustCompile(`BDUSS=[^;]+;`)

func (c *commandContext) loginInput() (loginInput, error) {
	in := loginInput{
		bduss:    strings.TrimSpace(c.flags.String("bduss")),
		ptoken:   strings.TrimSpace(c.flags.String("ptoken")),
		stoken:   strings.TrimSpace(c.flags.String("stoken")),
		cookies:  strings.TrimSpace(c.flags.String("cookies")),
		username: strings.TrimSpace(c.flags.String("username")),
		password: c.flags.String("password"),
	}
	if in.cookies != "" && (in.bduss != "" || in.stoken != "" || in.ptoken != "") {
		return in, usageErrorf("--cookies cannot be combined with --bduss, --ptoken or --stoken")
	}
	if in.cookies != "" && !cookieBDUSS.MatchString(in.cookies) {
		return in, usageErrorf("--cookies must contain a complete 'BDUSS=...;' field")
	}
	if in.bduss == "" && in.cookies == "" {
		if in.ptoken != "" || in.stoken != "" {
			return in, usageErrorf("--ptoken/--stoken require --bduss")
		}
		bduss, ptoken, stoken, cookies, err := pcscommand.RunLogin(in.username, in.password)
		if err != nil {
			return in, operationError(err)
		}
		in.bduss, in.ptoken, in.stoken, in.cookies = bduss, ptoken, stoken, cookies
	}
	return in, nil
}

func applyLogin(in loginInput) (*pcsconfig.Baidu, error) {
	return pcsconfig.Config.SetupUserByBDUSS(in.bduss, in.ptoken, in.stoken, in.cookies)
}

func validAlias(name string) bool { return aliasPattern.MatchString(name) }

func runConfigCreate(c *commandContext) error {
	name := c.args[0]
	if !validAlias(name) {
		return usageErrorf("remote names need at least two letters/digits, start with a letter, and may contain _ or -")
	}
	if _, exists := c.app.Resolver.Aliases[name]; exists {
		return operationError(fmt.Errorf("remote %q already exists; use 'config reconnect' instead", name))
	}
	in, err := c.loginInput()
	if err != nil {
		return err
	}
	user, err := applyLogin(in)
	if err != nil {
		return operationError(err)
	}
	for other, uid := range c.app.Resolver.Aliases {
		if uid == user.UID {
			if reloadErr := pcsconfig.Config.Reload(); reloadErr != nil {
				return operationError(fmt.Errorf("account is already configured as remote %q; reload failed: %w", other, reloadErr))
			}
			return operationError(fmt.Errorf("account is already configured as remote %q", other))
		}
	}
	c.app.Resolver.Aliases[name] = user.UID
	if err = pcsconfig.Config.Save(); err != nil {
		return operationError(err)
	}
	if err = c.app.Resolver.Save(); err != nil {
		return operationError(err)
	}
	fmt.Fprintf(c.app.Stdout, "Created remote %q for %s (uid %d)\n", name, user.Name, user.UID)
	return nil
}

func runConfigReconnect(c *commandContext) error {
	name := c.args[0]
	uid, exists := c.app.Resolver.Aliases[name]
	if !exists {
		return operationError(fmt.Errorf("unknown remote %q", name))
	}
	in, err := c.loginInput()
	if err != nil {
		return err
	}
	user, err := applyLogin(in)
	if err != nil {
		if reloadErr := pcsconfig.Config.Reload(); reloadErr != nil {
			return operationError(errors.Join(err, reloadErr))
		}
		return operationError(err)
	}
	if user.UID != uid {
		if reloadErr := pcsconfig.Config.Reload(); reloadErr != nil {
			return operationError(errors.Join(fmt.Errorf("login returned uid %d, expected %d", user.UID, uid), reloadErr))
		}
		return operationError(fmt.Errorf("login returned uid %d, expected %d; remote %q was not changed", user.UID, uid, name))
	}
	if err = pcsconfig.Config.Save(); err != nil {
		return operationError(err)
	}
	fmt.Fprintf(c.app.Stdout, "Reconnected remote %q for %s (uid %d)\n", name, user.Name, user.UID)
	return nil
}

func runConfigRename(c *commandContext) error {
	oldName, newName := c.args[0], c.args[1]
	if oldName == newName {
		fmt.Fprintf(c.app.Stdout, "Remote %q is already named %q\n", oldName, newName)
		return nil
	}
	if !validAlias(newName) {
		return usageErrorf("remote names need at least two letters/digits, start with a letter, and may contain _ or -")
	}
	uid, exists := c.app.Resolver.Aliases[oldName]
	if !exists {
		return operationError(fmt.Errorf("unknown remote %q", oldName))
	}
	if _, exists = c.app.Resolver.Aliases[newName]; exists {
		return operationError(fmt.Errorf("remote %q already exists", newName))
	}
	delete(c.app.Resolver.Aliases, oldName)
	c.app.Resolver.Aliases[newName] = uid
	if err := c.app.Resolver.Save(); err != nil {
		return operationError(err)
	}
	fmt.Fprintf(c.app.Stdout, "Renamed remote %q to %q\n", oldName, newName)
	return nil
}

func runConfigDelete(c *commandContext) error {
	name := c.args[0]
	uid, exists := c.app.Resolver.Aliases[name]
	if !exists {
		return operationError(fmt.Errorf("unknown remote %q", name))
	}
	delete(c.app.Resolver.Aliases, name)

	removeAccount := !c.flags.Bool("keep-account")
	if removeAccount {
		for _, otherUID := range c.app.Resolver.Aliases {
			if otherUID == uid {
				removeAccount = false
				break
			}
		}
	}
	if err := c.app.Resolver.Save(); err != nil {
		return operationError(err)
	}
	if removeAccount {
		_, err := pcsconfig.Config.DeleteUser(&pcsconfig.BaiduBase{UID: uid})
		if err != nil && !errors.Is(err, pcsconfig.ErrBaiduUserNotFound) && !errors.Is(err, pcsconfig.ErrNoSuchBaiduUser) {
			return operationError(err)
		}
		if err = pcsconfig.Config.Save(); err != nil {
			return operationError(err)
		}
		fmt.Fprintf(c.app.Stdout, "Removed remote %q and its stored account\n", name)
		return nil
	}
	fmt.Fprintf(c.app.Stdout, "Removed remote %q; account credentials retained\n", name)
	return nil
}

type remoteSummary struct {
	Name        string `json:"name"`
	UID         uint64 `json:"uid"`
	Username    string `json:"username"`
	BDUSS       bool   `json:"bduss"`
	STOKEN      bool   `json:"stoken"`
	Cookies     bool   `json:"cookies"`
	AccessToken bool   `json:"access_token"`
	Broken      bool   `json:"broken,omitempty"`
}

func findBaiduUser(uid uint64) *pcsconfig.Baidu {
	for _, user := range pcsconfig.Config.BaiduUserList {
		if user != nil && user.UID == uid {
			return user
		}
	}
	return nil
}

func runConfigShow(c *commandContext) error {
	names := make([]string, 0, len(c.app.Resolver.Aliases))
	if len(c.args) == 1 {
		name := c.args[0]
		if _, exists := c.app.Resolver.Aliases[name]; !exists {
			return operationError(fmt.Errorf("unknown remote %q", name))
		}
		names = append(names, name)
	} else {
		for name := range c.app.Resolver.Aliases {
			names = append(names, name)
		}
		sort.Strings(names)
	}

	summaries := make([]remoteSummary, 0, len(names))
	broken := 0
	for _, name := range names {
		uid := c.app.Resolver.Aliases[name]
		summary := remoteSummary{Name: name, UID: uid}
		user := findBaiduUser(uid)
		if user == nil {
			summary.Username = "<missing-account>"
			summary.Broken = true
			broken++
		} else {
			summary.Username = user.Name
			summary.BDUSS = user.BDUSS != ""
			summary.STOKEN = user.STOKEN != ""
			summary.Cookies = user.COOKIES != ""
			summary.AccessToken = user.AccessToken != ""
		}
		summaries = append(summaries, summary)
	}
	if c.flags.Bool("json") {
		if err := json.NewEncoder(c.app.Stdout).Encode(summaries); err != nil {
			return operationError(err)
		}
	} else {
		for i, summary := range summaries {
			if i > 0 {
				fmt.Fprintln(c.app.Stdout)
			}
			fmt.Fprintf(c.app.Stdout, "name: %s\nuid: %d\nusername: %s\nbduss: %s\nstoken: %s\ncookies: %s\naccess_token: %s\n",
				summary.Name, summary.UID, summary.Username,
				presence(summary.BDUSS), presence(summary.STOKEN), presence(summary.Cookies), presence(summary.AccessToken))
		}
	}
	if broken > 0 {
		return operationError(fmt.Errorf("%d remote(s) refer to accounts missing from the configuration", broken))
	}
	return nil
}

func presence(ok bool) string {
	if ok {
		return "set"
	}
	return "not set"
}

func runConfigFile(c *commandContext) error {
	dir := pcsconfig.GetConfigDir()
	info := map[string]string{
		"config":  pcsconfig.Config.FilePath(),
		"remotes": filepath.Join(dir, "rc_remotes.json"),
		"state":   filepath.Join(dir, "rc-state"),
	}
	if c.flags.Bool("json") {
		return json.NewEncoder(c.app.Stdout).Encode(info)
	}
	fmt.Fprintf(c.app.Stdout, "Config: %s\nRemotes: %s\nState: %s\n", info["config"], info["remotes"], info["state"])
	return nil
}

type legacyConfig struct {
	BaiduActiveUID uint64                  `json:"baidu_active_uid"`
	BaiduUserList  pcsconfig.BaiduUserList `json:"baidu_user_list"`
}

func runConfigImport(c *commandContext) error {
	source, err := resolveLegacyConfig(c.flags.String("from"), c.app.ConfigFile)
	if err != nil {
		return operationError(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return operationError(err)
	}
	var legacy legacyConfig
	if err = json.Unmarshal(data, &legacy); err != nil {
		return operationError(fmt.Errorf("parse legacy config %s: %w", source, err))
	}

	legacyAliases := map[string]uint64{}
	aliasPath := filepath.Join(filepath.Dir(source), "rc_remotes.json")
	if aliasData, readErr := os.ReadFile(aliasPath); readErr == nil {
		if unmarshalErr := json.Unmarshal(aliasData, &legacyAliases); unmarshalErr != nil {
			fmt.Fprintf(c.app.Stderr, "pcsclone: ignoring invalid legacy aliases %s: %v\n", aliasPath, unmarshalErr)
			legacyAliases = map[string]uint64{}
		}
	} else if !os.IsNotExist(readErr) {
		fmt.Fprintf(c.app.Stderr, "pcsclone: cannot read legacy aliases %s: %v\n", aliasPath, readErr)
	}

	usedAliases := make(map[string]bool, len(c.app.Resolver.Aliases))
	for name := range c.app.Resolver.Aliases {
		usedAliases[name] = true
	}
	knownUIDs := map[uint64]bool{}
	for _, user := range pcsconfig.Config.BaiduUserList {
		if user != nil {
			knownUIDs[user.UID] = true
		}
	}

	imported, existing, skipped := 0, 0, 0
	valid := 0
	for _, user := range legacy.BaiduUserList {
		if user == nil || user.UID == 0 || (user.BDUSS == "" && !strings.Contains(user.COOKIES, "BDUSS=")) {
			skipped++
			continue
		}
		valid++
		if knownUIDs[user.UID] {
			existing++
			continue
		}
		copied := *user
		copied.Workdir = "/"
		pcsconfig.Config.BaiduUserList = append(pcsconfig.Config.BaiduUserList, &copied)
		knownUIDs[user.UID] = true
		imported++
	}
	if valid == 0 {
		return operationError(fmt.Errorf("no usable Baidu accounts found in %s", source))
	}

	addedAliases := 0
	aliasNames := make([]string, 0, len(legacyAliases))
	for name := range legacyAliases {
		aliasNames = append(aliasNames, name)
	}
	sort.Strings(aliasNames)
	for _, name := range aliasNames {
		uid := legacyAliases[name]
		if !validAlias(name) || usedAliases[name] || !knownUIDs[uid] {
			continue
		}
		c.app.Resolver.Aliases[name] = uid
		usedAliases[name] = true
		addedAliases++
	}
	for _, user := range pcsconfig.Config.BaiduUserList {
		if user == nil {
			continue
		}
		if aliasExistsForUID(c.app.Resolver.Aliases, user.UID) {
			continue
		}
		name := uniqueAlias(deriveAlias(user.Name, user.UID), usedAliases, user.UID)
		c.app.Resolver.Aliases[name] = user.UID
		usedAliases[name] = true
		addedAliases++
	}
	if pcsconfig.Config.BaiduActiveUID == 0 || !knownUIDs[pcsconfig.Config.BaiduActiveUID] {
		if len(pcsconfig.Config.BaiduUserList) > 0 && pcsconfig.Config.BaiduUserList[0] != nil {
			pcsconfig.Config.BaiduActiveUID = pcsconfig.Config.BaiduUserList[0].UID
		}
	}

	dryRun := c.flags.Bool("dry-run")
	if !dryRun {
		if err = pcsconfig.Config.Save(); err != nil {
			return operationError(err)
		}
		if err = c.app.Resolver.Save(); err != nil {
			return operationError(err)
		}
	}
	if c.flags.Bool("json") {
		return json.NewEncoder(c.app.Stdout).Encode(map[string]interface{}{
			"source":   source,
			"imported": imported,
			"existing": existing,
			"skipped":  skipped,
			"aliases":  addedAliases,
			"dry_run":  dryRun,
		})
	}
	fmt.Fprintf(c.app.Stdout, "Imported %d account(s); %d already present; %d unusable; %d alias(es) added.\nSource: %s\n",
		imported, existing, skipped, addedAliases, source)
	if dryRun {
		fmt.Fprintln(c.app.Stdout, "Dry run: no files were written.")
	}
	return nil
}

func aliasExistsForUID(aliases map[string]uint64, uid uint64) bool {
	for _, candidate := range aliases {
		if candidate == uid {
			return true
		}
	}
	return false
}

func resolveLegacyConfig(from, current string) (string, error) {
	if from != "" {
		info, err := os.Stat(from)
		if err != nil {
			return "", fmt.Errorf("legacy config %s: %w", from, err)
		}
		if info.IsDir() {
			from = filepath.Join(from, defaultConfigName)
		}
		abs, err := filepath.Abs(from)
		if err != nil {
			return "", err
		}
		return abs, nil
	}

	var candidates []string
	if dir := strings.TrimSpace(os.Getenv(pcsconfig.EnvConfigDir)); dir != "" {
		candidates = append(candidates, filepath.Join(dir, defaultConfigName))
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), defaultConfigName))
	}
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("APPDATA"); dir != "" {
			candidates = append(candidates, filepath.Join(dir, "BaiduPCS-Go", defaultConfigName))
		}
	} else if home := os.Getenv("HOME"); home != "" {
		candidates = append(candidates, filepath.Join(home, ".config", "BaiduPCS-Go", defaultConfigName))
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if seen[abs] || abs == current {
			continue
		}
		seen[abs] = true
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			return abs, nil
		}
	}
	return "", errors.New("no legacy BaiduPCS-Go configuration found; use --from PATH")
}

func deriveAlias(name string, uid uint64) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	alias := strings.Trim(b.String(), "_-")
	if alias == "" {
		return fmt.Sprintf("u%d", uid)
	}
	if !(alias[0] >= 'A' && alias[0] <= 'Z') && !(alias[0] >= 'a' && alias[0] <= 'z') {
		alias = "u" + alias
	}
	if len(alias) < 2 {
		return fmt.Sprintf("u%d", uid)
	}
	if len(alias) > 48 {
		alias = alias[:48]
	}
	return alias
}

func uniqueAlias(base string, used map[string]bool, uid uint64) string {
	if !used[base] {
		return base
	}
	candidate := fmt.Sprintf("%s-%d", base, uid)
	if !used[candidate] {
		return candidate
	}
	for i := 2; ; i++ {
		candidate = fmt.Sprintf("%s-%d-%d", base, uid, i)
		if !used[candidate] {
			return candidate
		}
	}
}
