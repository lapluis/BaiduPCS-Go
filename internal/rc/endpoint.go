package rc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

var aliasPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]+$`)

type Resolver struct {
	CWD     string
	Aliases map[string]uint64
}

func aliasesPath() string { return filepath.Join(pcsconfig.GetConfigDir(), "rc_remotes.json") }
func NewResolver(cwd string) (*Resolver, error) {
	r := &Resolver{CWD: cwd, Aliases: map[string]uint64{}}
	b, err := os.ReadFile(aliasesPath())
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, &r.Aliases); err != nil {
		return nil, errors.New("invalid rc_remotes.json")
	}
	if r.Aliases == nil {
		r.Aliases = map[string]uint64{}
	}
	return r, nil
}
func (r *Resolver) Save() error {
	b, err := json.MarshalIndent(r.Aliases, "", "  ")
	if err != nil {
		return err
	}
	target, err := pcsconfig.ResolveConfigDestination(aliasesPath())
	if err != nil {
		return err
	}
	if pcsconfig.Config != nil && pcsconfig.Config.FilePath() != "" {
		account, err := pcsconfig.ResolveConfigDestination(pcsconfig.Config.FilePath())
		if err != nil {
			return err
		}
		overlap, err := localPathsOverlap(filepath.ToSlash(target), filepath.ToSlash(account))
		if err != nil {
			return err
		}
		aliasInfo, aliasErr := os.Stat(target)
		accountInfo, accountErr := os.Stat(account)
		for _, err := range []error{aliasErr, accountErr} {
			if err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if overlap || aliasErr == nil && accountErr == nil && os.SameFile(aliasInfo, accountInfo) {
			return errors.New("remote alias storage overlaps account configuration")
		}
	}
	// Publish to the inspected physical target, preserving an alias symlink.
	return pcsconfig.SaveConfigData(target, b)
}
func IsRemote(s string) bool {
	i := strings.IndexByte(s, ':')
	return i > 1 && !strings.ContainsAny(s[:i], `/\`)
}

// ResolveLocalPath resolves relative paths against cwd. On Windows a leading
// separator anchors to cwd's volume root, rather than its current subdirectory.
// Drive-relative paths depend on per-drive process state and remain unsupported.
func ResolveLocalPath(cwd, p string) (string, error) {
	if p == "" {
		return "", errors.New("empty path")
	}
	if localVolumeName(p) != "" && !filepath.IsAbs(p) {
		return "", errors.New("drive-relative paths are ambiguous; use an absolute path")
	}
	if !filepath.IsAbs(p) {
		if runtime.GOOS == "windows" && os.IsPathSeparator(p[0]) {
			if !filepath.IsAbs(cwd) {
				return "", errors.New("root-relative path requires an absolute working directory")
			}
			p = localVolumeName(cwd) + p
		} else {
			p = localJoinPath(cwd, p)
		}
	}
	return localCleanPath(p), nil
}

func (r *Resolver) Resolve(s string) (Endpoint, error) {
	if s == "" {
		return Endpoint{}, errors.New("empty path")
	}
	if !IsRemote(s) {
		var err error
		s, err = ResolveLocalPath(r.CWD, s)
		if err != nil {
			return Endpoint{}, err
		}
		if err := safeParents(s); err != nil {
			return Endpoint{}, err
		}
		return Endpoint{FS: &Local{}, Root: s}, nil
	}
	alias, rest, _ := strings.Cut(s, ":")
	uid, ok := r.Aliases[alias]
	if !ok {
		return Endpoint{}, fmt.Errorf("unknown remote %q; use remote add", alias)
	}
	if strings.Contains(rest, `\`) {
		return Endpoint{}, errors.New("remote paths use forward slashes")
	}
	for _, u := range pcsconfig.Config.BaiduUserList {
		if u != nil && u.UID == uid {
			return Endpoint{FS: &Remote{PCS: u.BaiduPCS(), UID: uid}, Root: path.Clean("/" + rest)}, nil
		}
	}
	return Endpoint{}, fmt.Errorf("remote %q refers to an account that is no longer logged in", alias)
}
