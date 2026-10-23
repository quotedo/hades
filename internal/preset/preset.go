// Package preset detects what kind of project a worktree holds and the
// default setup steps for it.
package preset

import (
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
	HasEcto  bool
	Tidewave bool

	// dir is the directory Detect looked at; Apply uses it to re-derive
	// setup defaults when the kind is overridden.
	dir string
}

var (
	appRe        = regexp.MustCompile(`\bapp:\s*:([A-Za-z_][A-Za-z0-9_]*)`)
	setupAliasRe = regexp.MustCompile(`\bsetup:\s*\[`)
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
// accepts `name: [` anywhere in the file rather than parsing aliases/0.
func mixHasAlias(mixExs []byte, name string) bool {
	if name == "setup" {
		return setupAliasRe.Match(mixExs)
	}
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `:\s*\[`).Match(mixExs)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Detect inspects dir: phoenix, elixir, go, node, else none.
func Detect(dir string) (Preset, error) {
	p := Preset{Kind: None, dir: dir}
	mixPath := filepath.Join(dir, "mix.exs")
	switch {
	case exists(mixPath):
		mix, err := os.ReadFile(mixPath)
		if err != nil {
			return Preset{}, err
		}
		app, err := MixApp(mix)
		if err != nil {
			return Preset{}, err
		}
		p.AppName = app
		p.HasEcto = mixHasDep(mix, "ecto_sql")
		p.Kind = Elixir
		if mixHasDep(mix, "phoenix") {
			p.Kind = Phoenix
		}
		lock, err := os.ReadFile(filepath.Join(dir, "mix.lock"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Preset{}, err
		}
		p.Tidewave = regexp.MustCompile(`"tidewave"`).Match(lock)
	case exists(filepath.Join(dir, "go.mod")):
		p.Kind = Go
	case exists(filepath.Join(dir, "package.json")):
		p.Kind = Node
	}
	p.derive()
	return p, nil
}

// derive sets Web and Setup to the defaults of p.Kind.
func (p *Preset) derive() {
	p.Web = p.Kind == Phoenix
	p.Setup = nil
	switch p.Kind {
	case Phoenix, Elixir:
		// A missing mix.exs (kind forced by hades.toml) means no setup alias.
		mix, _ := os.ReadFile(filepath.Join(p.dir, "mix.exs"))
		if mixHasAlias(mix, "setup") {
			p.Setup = []string{"mix setup"}
		} else {
			p.Setup = []string{"mix deps.get"}
		}
	case Go:
		p.Setup = []string{"go mod download"}
	case Node:
		switch {
		case exists(filepath.Join(p.dir, "pnpm-lock.yaml")):
			p.Setup = []string{"pnpm install --frozen-lockfile"}
		case exists(filepath.Join(p.dir, "yarn.lock")):
			p.Setup = []string{"yarn install --frozen-lockfile"}
		default:
			p.Setup = []string{"npm ci"}
		}
	}
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
		p.derive()
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
