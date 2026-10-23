package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/preset"
)

func toMap(kv []KV) map[string]string {
	m := map[string]string{}
	for _, e := range kv {
		m[e.Key] = e.Value
	}
	return m
}

func TestUpdateEnvrcPreservesUserLinesAndIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".envrc")
	os.WriteFile(p, []byte("use asdf\nexport MINE=1\n"), 0o644)
	kv := []KV{{"PORT", "4100"}, {"DB_SUFFIX", "_roman_x"}}
	if err := UpdateEnvrc(p, kv); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(p)
	want := "use asdf\nexport MINE=1\n# hades:begin\nexport PORT=4100\nexport DB_SUFFIX=_roman_x\n# hades:end\n"
	if string(first) != want {
		t.Fatalf("%q", first)
	}
	if err := UpdateEnvrc(p, kv); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(p)
	if string(second) != want {
		t.Fatal("not idempotent")
	}
	UpdateEnvrc(p, []KV{{"PORT", "4101"}})
	third, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(third), "use asdf\nexport MINE=1\n# hades:begin\nexport PORT=4101\n# hades:end\n") || strings.Contains(string(third), "DB_SUFFIX") {
		t.Fatalf("%q", third)
	}
}

func TestEnvWorktreeRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env.worktree")
	WriteEnvWorktree(p, []KV{{"A", "1"}, {"B", "two words"}})
	m, _ := ReadEnvWorktree(p)
	if m["A"] != "1" || m["B"] != "two words" {
		t.Fatal(m)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatal(st.Mode())
	}
}

func TestComputeServerPhoenix(t *testing.T) {
	h := config.Host{Domain: "example.test", Postgres: config.Postgres{Host: "127.0.0.1", Port: 5433, User: "postgres", Password: "postgres"}, RabbitMQ: &config.RabbitMQ{AMQPURL: "amqp://u:p@mq.test:5672"}}
	kv, err := Compute(Inputs{Port: 4100, Label: "jarl-x", Host: "jarl-x.example.test", URL: "https://jarl-x.example.test", Suffix: "_roman_x", Name: "x", Project: "jarl", User: "roman", Preset: preset.Preset{Kind: preset.Phoenix, AppName: "gym_nation"}, HostCfg: h, Vhost: "jarl-x", Extra: map[string]string{"JARL_ENDPOINT": "{{host}}"}, ExistingSecret: "keep"})
	if err != nil {
		t.Fatal(err)
	}
	m := toMap(kv)
	if m["PORT"] != "4100" || m["DB_PORT"] != "5433" || m["DATABASE_URL"] != "postgres://postgres:postgres@127.0.0.1:5433/gym_nation_dev_roman_x" || m["TEST_DATABASE_URL"] != "postgres://postgres:postgres@127.0.0.1:5433/gym_nation_test_roman_x" || m["SECRET_KEY_BASE"] != "keep" || m["PHX_HOST"] != "jarl-x.example.test" || m["JARL_ENDPOINT"] != "jarl-x.example.test" || m["RABBITMQ_VHOST"] != "jarl-x" || m["AMQP_URL"] != "amqp://u:p@mq.test:5672/jarl-x" {
		t.Fatalf("%v", m)
	}
	if kv[0].Key != "PORT" {
		t.Fatal("order")
	}
}

func TestComputeGeneratesSecretAndRejectsUnknownPlaceholder(t *testing.T) {
	kv, _ := Compute(Inputs{Port: 1, Preset: preset.Preset{Kind: preset.Go}})
	if s := toMap(kv)["SECRET_KEY_BASE"]; len(s) < 80 {
		t.Fatal(s)
	}
	if _, err := Compute(Inputs{Port: 1, Extra: map[string]string{"X": "{{nope}}"}}); err == nil {
		t.Fatal("accepted")
	}
}
