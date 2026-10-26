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
// neither exists it returns an empty Project. Unknown keys are an error.
func LoadProject(worktree, root string) (*Project, error) {
	for _, dir := range []string{worktree, root} {
		path := filepath.Join(dir, "hades.toml")
		var p Project
		md, err := toml.DecodeFile(path, &p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := unknownKeys(path, md); err != nil {
			return nil, err
		}
		return &p, nil
	}
	return &Project{}, nil
}

// unknownKeys fails naming the keys of the file at path that md did not
// decode.
func unknownKeys(path string, md toml.MetaData) error {
	un := md.Undecoded()
	if len(un) == 0 {
		return nil
	}
	keys := make([]string, len(un))
	for i, k := range un {
		keys[i] = k.String()
	}
	return fmt.Errorf("%s: unknown keys: %s", path, strings.Join(keys, ", "))
}
