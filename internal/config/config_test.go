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
	h, err := LaptopHost("/home/u", func(k string) string {
		if k == "HADES_DB_PORT" {
			return "5444"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.PortsDir != "/home/u/.local/state/hades/ports" || h.Postgres.Port != 5444 || h.Postgres.User != "postgres" || h.Domain != "" {
		t.Fatalf("%+v", h)
	}
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLaptopRejectsBadValues(t *testing.T) {
	if _, err := LaptopHost("/h", env(map[string]string{"HADES_DB_PORT": "abc"})); err == nil {
		t.Fatal("bad HADES_DB_PORT accepted")
	}
	if _, err := LaptopHost("/h", env(map[string]string{"HADES_PORT_RANGE": "5000-4000"})); err == nil {
		t.Fatal("bad HADES_PORT_RANGE accepted")
	}
	if _, _, err := Load(env(map[string]string{"HADES_HOST_CONFIG": "laptop", "HADES_PORT_RANGE": "x"}), "/h"); err == nil {
		t.Fatal("Load accepted bad HADES_PORT_RANGE")
	}
}

func TestLoadLaptopOverride(t *testing.T) {
	_, mode, err := Load(env(map[string]string{"HADES_HOST_CONFIG": "laptop"}), "/home/u")
	if err != nil || mode != Laptop {
		t.Fatal(mode, err)
	}
}

func TestLoadExplicitMissingPathIsError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nope.toml")
	_, _, err := Load(env(map[string]string{"HADES_HOST_CONFIG": p}), "/home/u")
	if !errors.Is(err, ErrNoHost) || !strings.Contains(err.Error(), p) {
		t.Fatal(err)
	}
}

func TestLoadProjectWorktreeFirstThenRoot(t *testing.T) {
	root, wt := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(root, "hades.toml"), []byte("project = \"fromroot\"\n"), 0o644)
	p, _ := LoadProject(wt, root)
	if p.Project != "fromroot" {
		t.Fatal(p)
	}
	os.WriteFile(filepath.Join(wt, "hades.toml"), []byte("project = \"fromwt\"\n"), 0o644)
	p, _ = LoadProject(wt, root)
	if p.Project != "fromwt" {
		t.Fatal(p)
	}
	p, err := LoadProject(t.TempDir(), t.TempDir())
	if err != nil || p == nil || p.Project != "" {
		t.Fatal(p, err)
	}
}

func TestLoadProjectExample(t *testing.T) {
	data, _ := os.ReadFile("testdata/hades.toml")
	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, "hades.toml"), data, 0o644)
	p, err := LoadProject(wt, wt)
	if err != nil || p.Project != "shop" || p.Env["SHOP_ENDPOINT"] != "{{host}}" || !p.Services.RabbitMQ.Vhost || p.Databases[0] != "shop" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestLoadProjectRejectsUnknownKeys(t *testing.T) {
	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, "hades.toml"), []byte("projct = \"x\"\n"), 0o644)
	if _, err := LoadProject(wt, wt); err == nil {
		t.Fatal("unknown key accepted")
	}
}
