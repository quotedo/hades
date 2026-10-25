package preset

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/quotedo/hades/internal/config"
)

func detect(t *testing.T, dir string) Preset {
	t.Helper()
	p, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect(%s): %v", dir, err)
	}
	return p
}

func apply(t *testing.T, p Preset, cfg *config.Project) Preset {
	t.Helper()
	q, err := Apply(p, cfg)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return q
}

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDetectPhoenix(t *testing.T) {
	p := detect(t, "testdata/phoenix") // mix.exs: app: :demo, deps {:phoenix, ...}, aliases setup: [...]; mix.lock mentions "tidewave"
	if p.Kind != Phoenix || !p.Web || p.AppName != "demo" || !p.Tidewave || !slices.Equal(p.Setup, []string{"mix setup"}) {
		t.Fatalf("%+v", p)
	}
}

func TestDetectElixirWithoutSetupAlias(t *testing.T) {
	p := detect(t, "testdata/elixir") // mix.exs without :phoenix and without setup alias
	if p.Kind != Elixir || p.Web || !slices.Equal(p.Setup, []string{"mix deps.get"}) || p.Tidewave {
		t.Fatalf("%+v", p)
	}
}

func TestDetectGoNodeNone(t *testing.T) {
	if p := detect(t, "testdata/go"); p.Kind != Go || !slices.Equal(p.Setup, []string{"go mod download"}) || p.Web {
		t.Fatalf("%+v", p)
	}
	if p := detect(t, "testdata/node-pnpm"); p.Kind != Node || !slices.Equal(p.Setup, []string{"pnpm install --frozen-lockfile"}) {
		t.Fatalf("%+v", p)
	}
	if p := detect(t, "testdata/none"); p.Kind != None || len(p.Setup) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestDetectNodeLockfiles(t *testing.T) {
	yarn := detect(t, writeFiles(t, map[string]string{"package.json": "{}", "yarn.lock": ""}))
	if !slices.Equal(yarn.Setup, []string{"yarn install --frozen-lockfile"}) {
		t.Fatalf("%+v", yarn)
	}
	npm := detect(t, writeFiles(t, map[string]string{"package.json": "{}"}))
	if !slices.Equal(npm.Setup, []string{"npm ci"}) {
		t.Fatalf("%+v", npm)
	}
}

func TestDetectPhoenixHTMLIsNotPhoenix(t *testing.T) {
	dir := writeFiles(t, map[string]string{"mix.exs": "[app: :x, deps: [{:phoenix_html, \"~> 4.0\"}]]"})
	if p := detect(t, dir); p.Kind != Elixir {
		t.Fatalf("%+v", p)
	}
}

func TestDetectStringSetupAlias(t *testing.T) {
	dir := writeFiles(t, map[string]string{"mix.exs": "[app: :x, aliases: [setup: \"deps.get\"]]"})
	if p := detect(t, dir); !slices.Equal(p.Setup, []string{"mix setup"}) {
		t.Fatalf("%+v", p)
	}
}

func TestDetectMixWithoutAppFails(t *testing.T) {
	dir := writeFiles(t, map[string]string{"mix.exs": "[deps: []]"})
	if p, err := Detect(dir); err == nil {
		t.Fatalf("want error, got %+v", p)
	}
}

func TestDetectUnreadableMixFails(t *testing.T) {
	// A directory named mix.exs exists but cannot be read as a file.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "mix.exs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, err := Detect(dir); err == nil {
		t.Fatalf("want error, got %+v", p)
	}
}

func TestApplyOverrides(t *testing.T) {
	web := true
	p := apply(t, Preset{Kind: Go}, &config.Project{Web: &web, Setup: []string{"make deps"}})
	if !p.Web || !slices.Equal(p.Setup, []string{"make deps"}) {
		t.Fatalf("%+v", p)
	}
	p = apply(t, Preset{Kind: Node}, &config.Project{Preset: "none"})
	if p.Kind != None || len(p.Setup) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestApplyTidewaveOverride(t *testing.T) {
	off := false
	p := apply(t, Preset{Kind: Phoenix, Tidewave: true}, &config.Project{MCP: config.MCP{Tidewave: &off}})
	if p.Tidewave {
		t.Fatalf("%+v", p)
	}
	on := true
	p = apply(t, Preset{Kind: Phoenix}, &config.Project{MCP: config.MCP{Tidewave: &on}})
	if !p.Tidewave {
		t.Fatalf("%+v", p)
	}
}

func TestApplyForcedElixirKindsRederive(t *testing.T) {
	p := detect(t, "testdata/phoenix")
	p = apply(t, p, &config.Project{Preset: "elixir"})
	if p.Kind != Elixir || p.Web || !slices.Equal(p.Setup, []string{"mix setup"}) {
		t.Fatalf("%+v", p)
	}
	g := detect(t, "testdata/go")
	g = apply(t, g, &config.Project{Preset: "phoenix"})
	if g.Kind != Phoenix || !g.Web || !slices.Equal(g.Setup, []string{"mix deps.get"}) {
		t.Fatalf("%+v", g)
	}
}

func TestApplyUnknownPreset(t *testing.T) {
	if p, err := Apply(Preset{Kind: Go}, &config.Project{Preset: "rails"}); err == nil {
		t.Fatalf("want error, got %+v", p)
	}
}
