package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Services struct {
	RabbitMQ struct{ Vhost bool }
}

type MCP struct{ Tidewave *bool }

// Project is the optional per-repository hades.toml. Every key is optional.
type Project struct {
	Project   string
	Preset    string
	Web       *bool
	Setup     []string
	Teardown  []string
	Databases []string // base names before the worktree suffix, e.g. shop_dev; default <app>_dev, <app>_test
	Env       map[string]string
	Services  Services
	MCP       MCP
}

// LoadProject reads <worktree>/hades.toml, else <root>/hades.toml. When
// neither exists it returns an empty Project and from == "". Unknown keys are
// an error.
func LoadProject(worktree, root string) (p *Project, from string, err error) {
	for _, dir := range []string{worktree, root} {
		path := filepath.Join(dir, "hades.toml")
		var q Project
		md, err := toml.DecodeFile(path, &q)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, path, err
		}
		if un := md.Undecoded(); len(un) > 0 {
			keys := make([]string, len(un))
			for i, k := range un {
				keys[i] = k.String()
			}
			return nil, path, fmt.Errorf("%s: unknown keys: %s", path, strings.Join(keys, ", "))
		}
		return &q, path, nil
	}
	return &Project{}, "", nil
}
