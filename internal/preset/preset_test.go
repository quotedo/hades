package preset

import (
	"testing"

	"github.com/quotedo/hades/internal/config"
)

func TestDetectPhoenix(t *testing.T) {
	p, err := Detect("testdata/phoenix") // mix.exs: app: :demo, deps {:phoenix, ...}, {:ecto_sql, ...}, aliases setup: [...]; mix.lock mentions "tidewave"
	if err != nil || p.Kind != Phoenix || !p.Web || p.AppName != "demo" || !p.HasEcto || !p.Tidewave || p.Setup[0] != "mix setup" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestDetectElixirWithoutSetupAlias(t *testing.T) {
	p, _ := Detect("testdata/elixir") // mix.exs without :phoenix and without setup alias
	if p.Kind != Elixir || p.Web || p.Setup[0] != "mix deps.get" || p.Tidewave {
		t.Fatalf("%+v", p)
	}
}

func TestDetectGoNodeNone(t *testing.T) {
	if p, _ := Detect("testdata/go"); p.Kind != Go || p.Setup[0] != "go mod download" || p.Web {
		t.Fatalf("%+v", p)
	}
	if p, _ := Detect("testdata/node-pnpm"); p.Kind != Node || p.Setup[0] != "pnpm install --frozen-lockfile" {
		t.Fatalf("%+v", p)
	}
	if p, _ := Detect("testdata/none"); p.Kind != None || len(p.Setup) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestApplyOverrides(t *testing.T) {
	web := true
	p, _ := Apply(Preset{Kind: Go}, &config.Project{Web: &web, Setup: []string{"make deps"}})
	if !p.Web || p.Setup[0] != "make deps" {
		t.Fatalf("%+v", p)
	}
	p, _ = Apply(Preset{Kind: Node}, &config.Project{Preset: "none"})
	if p.Kind != None || len(p.Setup) != 0 {
		t.Fatalf("%+v", p)
	}
}
