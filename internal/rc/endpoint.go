package rc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
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
	f, err := os.CreateTemp(filepath.Dir(aliasesPath()), ".rc-remotes-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, aliasesPath())
}
func IsRemote(s string) bool {
	i := strings.IndexByte(s, ':')
	return i > 1 && !strings.ContainsAny(s[:i], `/\`)
}
func (r *Resolver) Resolve(s string) (Endpoint, error) {
	if s == "" {
		return Endpoint{}, errors.New("empty path")
	}
	if !IsRemote(s) {
		if filepath.VolumeName(s) != "" && !filepath.IsAbs(s) {
			return Endpoint{}, errors.New("drive-relative paths are ambiguous; use an absolute path")
		}
		if !filepath.IsAbs(s) {
			s = filepath.Join(r.CWD, s)
		}
		s = filepath.Clean(s)
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
