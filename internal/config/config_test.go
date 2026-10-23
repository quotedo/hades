package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadHostValid(t *testing.T) {
	h, err := LoadHost("testdata/host.toml")
	if err != nil {
		t.Fatal(err)
	}
	if h.Domain != "example.test" || h.LabelTemplate != "{{project}}-{{name}}" || h.Postgres.Port != 5433 || h.RabbitMQ == nil {
		t.Fatalf("%+v", h)
	}
	if r := h.Range(); r.Lo != 4100 || r.Hi != 4999 {
		t.Fatal(r)
	}
}

func TestLoadHostMissingIsErrNoHost(t *testing.T) {
	_, err := LoadHost(filepath.Join(t.TempDir(), "none.toml"))
	if !errors.Is(err, ErrNoHost) {
		t.Fatal(err)
	}
}

func TestLoadHostRequiresKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "host.toml")
	os.WriteFile(p, []byte("domain = \"x\"\n"), 0o644)
	_, err := LoadHost(p)
	if err == nil || !strings.Contains(err.Error(), "port_range") {
		t.Fatal(err)
	}
}

func TestLaptopDefaults(t *testing.T) {
	h := LaptopHost("/home/u", func(k string) string {
		if k == "HADES_DB_PORT" {
			return "5444"
		}
		return ""
	})
	if h.PortsDir != "/home/u/.local/state/hades/ports" || h.Postgres.Port != 5444 || h.Postgres.User != "postgres" || h.Domain != "" {
		t.Fatalf("%+v", h)
	}
}

func TestLoadFallsBackToLaptop(t *testing.T) {
	getenv := func(k string) string {
		if k == "HADES_HOST_CONFIG" {
			return filepath.Join(t.TempDir(), "nope.toml")
		}
		return ""
	}
	_, mode, err := Load(getenv, "/home/u")
	if err != nil || mode != Laptop {
		t.Fatal(mode, err)
	}
}

func TestLoadProjectWorktreeFirstThenRoot(t *testing.T) {
	root, wt := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(root, "hades.toml"), []byte("project = \"fromroot\"\n"), 0o644)
	p, from, _ := LoadProject(wt, root)
	if p.Project != "fromroot" || from != filepath.Join(root, "hades.toml") {
		t.Fatal(p, from)
	}
	os.WriteFile(filepath.Join(wt, "hades.toml"), []byte("project = \"fromwt\"\n"), 0o644)
	p, _, _ = LoadProject(wt, root)
	if p.Project != "fromwt" {
		t.Fatal(p)
	}
	p, from, err := LoadProject(t.TempDir(), t.TempDir())
	if err != nil || from != "" || p == nil {
		t.Fatal(p, from, err)
	}
}

func TestLoadProjectExample(t *testing.T) {
	data, _ := os.ReadFile("testdata/hades.toml")
	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, "hades.toml"), data, 0o644)
	p, _, err := LoadProject(wt, wt)
	if err != nil || p.Project != "jarl" || p.Env["JARL_ENDPOINT"] != "{{host}}" || !p.Services.RabbitMQ.Vhost || p.Databases[0] != "gym_nation" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestLoadProjectRejectsUnknownKeys(t *testing.T) {
	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, "hades.toml"), []byte("projct = \"x\"\n"), 0o644)
	if _, _, err := LoadProject(wt, wt); err == nil {
		t.Fatal("unknown key accepted")
	}
}
