package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// useHostPath points DefaultHostPath at path for the test.
func useHostPath(t *testing.T, path string) {
	prev := DefaultHostPath
	DefaultHostPath = path
	t.Cleanup(func() { DefaultHostPath = prev })
}

var laptopPostgres = Postgres{"127.0.0.1", 5432, "postgres", "postgres"}

func TestLoadWithoutHostFileUsesLaptopDefaults(t *testing.T) {
	useHostPath(t, filepath.Join(t.TempDir(), "host.toml"))
	h, err := Load(env(nil), "/h")
	if err != nil {
		t.Fatal(err)
	}
	if h.Domain != "" || h.LabelTemplate != "{{project}}-{{name}}" || h.Ports != (PortRange{4100, 4999}) ||
		h.PortsDir != "/h/.local/state/hades/ports" || h.Postgres != laptopPostgres || h.RabbitMQ != nil {
		t.Fatalf("%+v", h)
	}
}

func TestLoadOverlaysHostFile(t *testing.T) {
	useHostPath(t, "testdata/host.toml")
	h, err := Load(env(nil), "/h")
	if err != nil {
		t.Fatal(err)
	}
	rmq := RabbitMQ{ManagementURL: "http://127.0.0.1:15672", AMQPURL: "amqp://guest:guest@127.0.0.1:5672", User: "guest", Password: "guest"}
	if h.Domain != "example.test" || h.ChromePath != "/opt/chrome-for-testing/chrome" || h.RabbitMQ == nil || *h.RabbitMQ != rmq ||
		h.LabelTemplate != "{{user}}-{{project}}-{{name}}" || h.Ports != (PortRange{4100, 4999}) ||
		h.PortsDir != "/h/.local/state/hades/ports" || h.Postgres != laptopPostgres {
		t.Fatalf("%+v", h)
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "host.toml")
	os.WriteFile(p, []byte("ports_dir = \"/srv/hades/ports\"\n"), 0o644)
	if _, err := Load(env(map[string]string{"HADES_HOST_CONFIG": p}), "/h"); err == nil || !strings.Contains(err.Error(), "ports_dir") {
		t.Fatal(err)
	}
}

func TestLoadExplicitMissingFileFails(t *testing.T) {
	if _, err := Load(env(map[string]string{"HADES_HOST_CONFIG": "/nonexistent"}), "/h"); err == nil {
		t.Fatal("missing explicit host config accepted")
	}
}

func TestLaptopDefaults(t *testing.T) {
	useHostPath(t, filepath.Join(t.TempDir(), "host.toml"))
	h, err := Load(env(map[string]string{"HADES_DB_PORT": "5444"}), "/home/u")
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
	if _, err := Load(env(map[string]string{"HADES_DB_PORT": "abc"}), "/h"); err == nil {
		t.Fatal("bad HADES_DB_PORT accepted")
	}
	if _, err := Load(env(map[string]string{"HADES_PORT_RANGE": "5000-4000"}), "/h"); err == nil {
		t.Fatal("bad HADES_PORT_RANGE accepted")
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
