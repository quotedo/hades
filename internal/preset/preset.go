// Package preset detects what kind of project a worktree holds and the
// default setup steps for it.
package preset

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/quotedo/hades/internal/config"
)

type Kind string

const (
	Phoenix Kind = "phoenix"
	Elixir  Kind = "elixir"
	Go      Kind = "go"
	Node    Kind = "node"
	None    Kind = "none"
)

// Preset is the detected shape of a project plus its derived defaults.
type Preset struct {
	Kind     Kind
	Web      bool
	Setup    []string
	AppName  string
	Tidewave bool

	// dir is the directory Detect looked at; Apply uses it to re-derive
	// setup defaults when the kind is overridden.
	dir string
}

var (
	appRe        = regexp.MustCompile(`\bapp:\s*:([A-Za-z_][A-Za-z0-9_]*)`)
	setupAliasRe = regexp.MustCompile(`\bsetup:\s*\S`)
)

// MixApp returns the `app: :name` atom from a mix.exs.
func MixApp(mixExs []byte) (string, error) {
	m := appRe.FindSubmatch(mixExs)
	if m == nil {
		return "", errors.New("mix.exs: no `app: :name` found")
	}
	return string(m[1]), nil
}

func mixHasDep(mixExs []byte, dep string) bool {
	re := regexp.MustCompile(`\{:` + regexp.QuoteMeta(dep) + `\s*,`)
	return re.Match(mixExs)
}

// mixHasAlias reports whether an alias of the given name is defined. It
// accepts `name:` followed by a value anywhere in the file rather than
// parsing aliases/0.
func mixHasAlias(mixExs []byte, name string) bool {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `:\s*\S`).Match(mixExs)
}

// exists reports whether path exists; only not-exist counts as absent.
func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// Detect inspects dir: phoenix, elixir, go, node, else none.
func Detect(dir string) (Preset, error) {
	p := Preset{Kind: None, dir: dir}
	for _, c := range []struct {
		file string
		kind Kind
	}{{"mix.exs", Elixir}, {"go.mod", Go}, {"package.json", Node}} {
		ok, err := exists(filepath.Join(dir, c.file))
		if err != nil {
			return Preset{}, err
		}
		if ok {
			p.Kind = c.kind
			break
		}
	}
	if p.Kind == Elixir {
		mix, err := os.ReadFile(filepath.Join(dir, "mix.exs"))
		if err != nil {
			return Preset{}, err
		}
		app, err := MixApp(mix)
		if err != nil {
			return Preset{}, err
		}
		p.AppName = app
		if mixHasDep(mix, "phoenix") {
			p.Kind = Phoenix
		}
		lock, err := os.ReadFile(filepath.Join(dir, "mix.lock"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Preset{}, err
		}
		p.Tidewave = bytes.Contains(lock, []byte(`"tidewave"`))
	}
	if err := p.derive(); err != nil {
		return Preset{}, err
	}
	return p, nil
}

// derive sets Web and Setup to the defaults of p.Kind.
func (p *Preset) derive() error {
	p.Web = p.Kind == Phoenix
	p.Setup = nil
	switch p.Kind {
	case Phoenix, Elixir:
		// A missing mix.exs (kind forced by hades.toml) means no setup alias.
		mix, err := os.ReadFile(filepath.Join(p.dir, "mix.exs"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if mixHasAlias(mix, "setup") {
			p.Setup = []string{"mix setup"}
		} else {
			p.Setup = []string{"mix deps.get"}
		}
	case Go:
		p.Setup = []string{"go mod download"}
	case Node:
		for _, l := range []struct{ file, cmd string }{
			{"pnpm-lock.yaml", "pnpm install --frozen-lockfile"},
			{"yarn.lock", "yarn install --frozen-lockfile"},
		} {
			ok, err := exists(filepath.Join(p.dir, l.file))
			if err != nil {
				return err
			}
			if ok {
				p.Setup = []string{l.cmd}
				return nil
			}
		}
		p.Setup = []string{"npm ci"}
	}
	return nil
}

// Apply layers hades.toml overrides over a detected preset. An unknown
// cfg.Preset is an error.
func Apply(p Preset, cfg *config.Project) (Preset, error) {
	if cfg.Preset != "" {
		k := Kind(cfg.Preset)
		switch k {
		case Phoenix, Elixir, Go, Node, None:
		default:
			return Preset{}, fmt.Errorf("unknown preset %q", cfg.Preset)
		}
		p.Kind = k
		if err := p.derive(); err != nil {
			return Preset{}, err
		}
	}
	if cfg.Web != nil {
		p.Web = *cfg.Web
	}
	if cfg.Setup != nil {
		p.Setup = cfg.Setup
	}
	if cfg.MCP.Tidewave != nil {
		p.Tidewave = *cfg.MCP.Tidewave
	}
	return p, nil
}
