package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/pg"
	"github.com/quotedo/hades/internal/registry"
	"github.com/quotedo/hades/internal/testutil"
)

// domainFixture writes a host config with domain example.test, plus extra,
// and returns the registry under HOME (created), a getenv pointing at both,
// and HOME. <HOME>/bin leads PATH, for stub tools. Postgres points at a
// closed port, so the postgres check fails fast.
func domainFixture(t *testing.T, extra string) (*registry.Registry, func(string) string, string) {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	hostFile := filepath.Join(home, "host.toml")
	if err := os.WriteFile(hostFile, []byte("domain = \"example.test\"\n"+extra), 0o644); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		switch k {
		case "HADES_HOST_CONFIG":
			return hostFile
		case "HADES_DB_PORT":
			return "1"
		case "PATH":
			return os.Getenv("PATH")
		case "HOME":
			return home
		}
		return ""
	}
	host, err := config.Load(getenv, home)
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New(host)
	if err := reg.EnsureDirs(0o700); err != nil {
		t.Fatal(err)
	}
	return reg, getenv, home
}

func runMain(getenv func(string) string, args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = Main(args, &out, &errb, getenv)
	return code, out.String(), errb.String()
}

func TestLsJSONAndTable(t *testing.T) {
	_, _, getenv := laptopUpFixture(t, "phoenix")
	reg := laptopRegistry(t, getenv)

	// An empty registry lists as [], not null.
	code, out, errs := runMain(getenv, "ls", "--json")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty: code=%d out=%q err=%q", code, out, errs)
	}

	for _, m := range []*registry.Manifest{
		{Label: "demo-a", Port: 4100, Host: "demo-a.example.test", Worktree: "/w/a", CreatedAt: time.Now().Add(-3 * time.Hour)},
		{Label: "demo-b", Port: 4101, Host: "demo-b.example.test", Worktree: "/w/b", CreatedAt: time.Now().Add(-49 * time.Hour)},
	} {
		if err := reg.WriteManifest(m); err != nil {
			t.Fatal(err)
		}
	}

	code, out, errs = runMain(getenv, "ls", "--json")
	var all []registry.Manifest
	if err := json.Unmarshal([]byte(out), &all); err != nil || code != 0 || len(all) != 2 || all[0].Label != "demo-a" {
		t.Fatalf("all: code=%d err=%v out=%q stderr=%q", code, err, out, errs)
	}

	code, out, _ = runMain(getenv, "ls")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) != 3 || strings.Join(strings.Fields(lines[0]), " ") != "LABEL PORT HOST WORKTREE AGE" ||
		!strings.HasSuffix(lines[1], "3h") || !strings.HasSuffix(lines[2], "2d") {
		t.Fatalf("table: code=%d out=%q", code, out)
	}
}

func TestPortFromPaseoEnv(t *testing.T) {
	reg, getenv, state := domainFixture(t, "")
	wt := filepath.Join(state, "wt")
	if err := os.Mkdir(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := reg.WriteManifest(&registry.Manifest{User: "u", Label: "demo-a", Port: 4100, Worktree: wt, Root: state}); err != nil {
		t.Fatal(err)
	}
	withPaseo := func(path string) func(string) string {
		return func(k string) string {
			switch k {
			case "PASEO_WORKTREE_PATH":
				return path
			case "PASEO_SOURCE_CHECKOUT_PATH":
				return state
			case "PASEO_BRANCH_NAME":
				return "feature/x"
			}
			return getenv(k)
		}
	}
	code, out, errs := runMain(withPaseo(wt), "port")
	if code != 0 || out != "4100\n" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}

	unknown := filepath.Join(state, "elsewhere")
	if err := os.Mkdir(unknown, 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, errs = runMain(withPaseo(unknown), "port")
	if code != 1 || out != "" || !strings.Contains(errs, "hades: port: no hades manifest for") || !strings.Contains(errs, "run hades up") {
		t.Fatalf("unknown: code=%d out=%q err=%q", code, out, errs)
	}

	// A non-web worktree has a manifest but no port.
	nonweb := filepath.Join(state, "nonweb")
	if err := os.Mkdir(nonweb, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := reg.WriteManifest(&registry.Manifest{User: "u", Label: "demo-nw", Worktree: nonweb, Root: state}); err != nil {
		t.Fatal(err)
	}
	code, _, errs = runMain(withPaseo(nonweb), "port")
	if code != 1 || !strings.Contains(errs, "hades: port: demo-nw has no web port") {
		t.Fatalf("non-web: code=%d err=%q", code, errs)
	}
}

func TestEnvShellOutput(t *testing.T) {
	root, wt, getenv := laptopUpFixture(t, "phoenix")
	args := func(more ...string) []string {
		return append([]string{"--root", root, "--worktree", wt, "--name", "feature/x"}, more...)
	}

	realWT, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}
	code, out, errs := runMain(getenv, args("env")...)
	if code != 1 || out != "" || !strings.Contains(errs, "hades: env: no .env.worktree in "+realWT+"; run hades up") {
		t.Fatalf("missing: code=%d out=%q err=%q", code, out, errs)
	}

	if err := os.WriteFile(filepath.Join(wt, ".env.worktree"), []byte("PORT=4100\nNAME=\"it's\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs = runMain(getenv, args("env")...)
	if code != 0 || out != "export PORT=4100\nexport NAME='it'\\''s'\n" {
		t.Fatalf("shell: code=%d out=%q err=%q", code, out, errs)
	}
	code, out, _ = runMain(getenv, args("env", "--json")...)
	var m map[string]string
	if err := json.Unmarshal([]byte(out), &m); err != nil || code != 0 || m["PORT"] != "4100" || m["NAME"] != "it's" {
		t.Fatalf("json: code=%d err=%v out=%q", code, err, out)
	}
}

func TestDoctorLaptopRegistryCreatedAtFirstUp(t *testing.T) {
	stubProbe(t)
	_, _, getenv := laptopUpFixture(t, "phoenix")
	t.Chdir(t.TempDir())
	_, checks, _ := doctorChecks(t, getenv)
	if c := checks["registry"]; !c.OK || c.Detail != "created at first up" || !checks["manifests"].OK {
		t.Fatalf("checks = %+v", checks)
	}
}

// doctor names the host config it loaded, and has no registry-perms or
// caddy-reload check.
func TestDoctorReportsHostConfig(t *testing.T) {
	stubProbe(t)
	_, _, getenv := laptopUpFixture(t, "phoenix")
	t.Chdir(t.TempDir())
	_, checks, _ := doctorChecks(t, getenv)
	if c := checks["host-config"]; !c.OK || c.Detail != "no host config: laptop defaults" {
		t.Fatalf("host-config = %+v", c)
	}
	hostFile := filepath.Join(t.TempDir(), "host.toml")
	if err := os.WriteFile(hostFile, []byte("domain = \"example.test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, checks, _ = doctorChecks(t, withEnv(getenv, map[string]string{"HADES_HOST_CONFIG": hostFile}))
	if c := checks["host-config"]; !c.OK || c.Detail != "loaded "+hostFile+" (domain example.test)" {
		t.Fatalf("host-config = %+v", c)
	}
	for _, name := range []string{"registry-perms", "caddy-reload"} {
		if c, ok := checks[name]; ok {
			t.Errorf("%s = %+v", name, c)
		}
	}
}

func TestDoctorPostgresFailureNamesTheAddress(t *testing.T) {
	_, getenv, _ := domainFixture(t, "")
	code, checks, _ := doctorChecks(t, getenv)
	if c := checks["postgres"]; code != 1 || c.OK || c.Warn || !strings.HasSuffix(c.Detail, ": start Postgres on 127.0.0.1:1") {
		t.Fatalf("code=%d postgres=%+v", code, c)
	}
}

func doctorChecks(t *testing.T, getenv func(string) string) (int, map[string]check, []string) {
	t.Helper()
	code, out, errs := runMain(getenv, "doctor", "--json")
	var list []check
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("doctor --json: %v: out=%q err=%q", err, out, errs)
	}
	byName := map[string]check{}
	var order []string
	for _, c := range list {
		byName[c.Name] = c
		order = append(order, c.Name)
	}
	return code, byName, order
}

func TestDoctorRabbitMQ(t *testing.T) {
	var gotUser, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, _, _ = r.BasicAuth()
		gotPath = r.URL.Path
	}))
	defer srv.Close()
	_, getenv, _ := domainFixture(t, "[rabbitmq]\nmanagement_url = \""+srv.URL+"\"\namqp_url = \"amqp://x\"\nuser = \"guest\"\npassword = \"guest\"\n")
	_, checks, _ := doctorChecks(t, getenv)
	if c := checks["rabbitmq"]; !c.OK || gotUser != "guest" || gotPath != "/api/overview" {
		t.Fatalf("rabbitmq = %+v user=%q path=%q", c, gotUser, gotPath)
	}
	srv.Close()
	_, checks, _ = doctorChecks(t, getenv)
	if c := checks["rabbitmq"]; c.OK || c.Warn {
		t.Fatalf("closed server: rabbitmq = %+v", c)
	}
}

func TestDoctorVersionsWarn(t *testing.T) {
	reg, getenv, _ := domainFixture(t, "")
	if err := reg.WriteManifest(&registry.Manifest{User: "u", Label: "demo-old", HadesVersion: "0.0.1-ancient", Worktree: "/w/old"}); err != nil {
		t.Fatal(err)
	}
	_, checks, _ := doctorChecks(t, getenv)
	if c := checks["versions"]; c.OK || !c.Warn || !strings.Contains(c.Detail, "demo-old") {
		t.Fatalf("versions = %+v", c)
	}
}

func TestStatusJSONShape(t *testing.T) {
	reg, getenv, state := domainFixture(t, "")
	wt := filepath.Join(state, "wt")
	if err := os.Mkdir(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := reg.WriteManifest(&registry.Manifest{User: "u", Label: "demo-a", Port: 4100, Worktree: wt}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(wt, filepath.Join(reg.PortsDir, "4100")); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runMain(getenv, "status", "--json")
	if code != 0 {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	var s struct {
		HostConfig string `json:"host_config"`
		Domain     string `json:"domain"`
		Ports      struct {
			Range string `json:"range"`
			Used  int    `json:"used"`
			Free  int    `json:"free"`
		} `json:"ports"`
		Worktrees int `json:"worktrees"`
		Postgres  struct {
			Reachable bool `json:"reachable"`
			Databases int  `json:"databases"`
		} `json:"postgres"`
	}
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if s.HostConfig != getenv("HADES_HOST_CONFIG") || s.Domain != "example.test" ||
		s.Ports.Range != "4100-4999" || s.Ports.Used != 1 || s.Ports.Free != 899 || s.Worktrees != 1 ||
		s.Postgres.Reachable {
		t.Fatalf("status = %+v", s)
	}

	code, out, _ = runMain(getenv, "status")
	if code != 0 || !strings.Contains(out, "host_config: "+getenv("HADES_HOST_CONFIG")+"\n") || !strings.Contains(out, "ports.used: "+strconv.Itoa(1)+"\n") {
		t.Fatalf("text: code=%d out=%q", code, out)
	}
}

func TestStatusOnLaptop(t *testing.T) {
	_, _, getenv := laptopUpFixture(t, "phoenix")
	code, out, errs := runMain(getenv, "status", "--json")
	if code != 0 || !strings.Contains(out, `"host_config": ""`) || !strings.Contains(out, `"domain": ""`) {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
}

// stubProbe makes the Postgres probe succeed for the test.
func stubProbe(t *testing.T) {
	t.Helper()
	prev := pgProbe
	pgProbe = func(context.Context, pg.Databases) ([]string, error) { return []string{"a", "b"}, nil }
	t.Cleanup(func() { pgProbe = prev })
}

// withEnv overlays getenv with fixed values.
func withEnv(getenv func(string) string, kv map[string]string) func(string) string {
	return func(k string) string {
		if v, ok := kv[k]; ok {
			return v
		}
		return getenv(k)
	}
}

// phoenixDoctorFixture is a host with a domain whose current worktree is a
// phoenix project. PATH holds only the stub dir (with git, and mix when withMix, in
// <home>/.asdf/shims), so direnv is absent.
func phoenixDoctorFixture(t *testing.T, withMix bool) (getenv func(string) string, reg *registry.Registry, wt, shims string) {
	t.Helper()
	stubProbe(t)
	var state string
	reg, getenv, state = domainFixture(t, "")
	_, wt, _ = laptopUpFixture(t, "phoenix")
	bin := filepath.Join(state, "bin")
	testutil.StubTool(t, bin, "git", "#!/bin/bash\nexit 0\n")
	shims = filepath.Join(state, ".asdf", "shims")
	if err := os.MkdirAll(shims, 0o755); err != nil {
		t.Fatal(err)
	}
	if withMix {
		testutil.StubTool(t, shims, "mix", "#!/bin/bash\nexit 0\n")
	}
	getenv = withEnv(getenv, map[string]string{"PATH": bin})
	return getenv, reg, wt, shims
}

func TestDoctorWarnOnlyFailuresKeepExitZero(t *testing.T) {
	getenv, reg, wt, shims := phoenixDoctorFixture(t, true)
	if err := reg.WriteManifest(&registry.Manifest{User: "u", Label: "demo-old", HadesVersion: "0.0.1-ancient", Worktree: "/w/old"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "paseo.json"), []byte(`{"setup":"echo hi"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"--root", filepath.Dir(wt), "--worktree", wt, "--name", "feature/x", "doctor"}
	code, out, errs := runMain(getenv, args...)
	if code != 0 {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
	for _, want := range []string{"WARN direnv:", "WARN versions:", "WARN ade-hooks: paseo.json", "ok   tools: " + shims, "ok   git:"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestDoctorWarnsWithoutADEConfig(t *testing.T) {
	getenv, _, wt, _ := phoenixDoctorFixture(t, true)
	code, out, errs := runMain(getenv, "--root", filepath.Dir(wt), "--worktree", wt, "--name", "feature/x", "doctor")
	if code != 0 || !strings.Contains(out, "WARN ade-hooks: no paseo.json or orca.yaml: run hades init and commit\n") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
}

func TestDoctorToolsFailWhenMixMissing(t *testing.T) {
	getenv, _, wt, _ := phoenixDoctorFixture(t, false)
	code, out, errs := runMain(getenv, "--root", filepath.Dir(wt), "--worktree", wt, "--name", "feature/x", "doctor")
	if code != 1 || !strings.Contains(out, "FAIL tools: phoenix preset needs mix") || !strings.Contains(out, ": install mix\n") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
}

func TestDoctorGitMissingFails(t *testing.T) {
	stubProbe(t)
	_, getenv, _ := domainFixture(t, "")
	getenv = withEnv(getenv, map[string]string{"PATH": t.TempDir()})
	code, checks, _ := doctorChecks(t, getenv)
	if code != 1 || checks["git"].OK || checks["git"].Warn {
		t.Fatalf("code=%d git=%+v", code, checks["git"])
	}
}

func TestDoctorOutsideRepoSkipsRepoChecks(t *testing.T) {
	stubProbe(t)
	_, getenv, _ := domainFixture(t, "")
	t.Chdir(t.TempDir())
	_, checks, order := doctorChecks(t, getenv)
	for _, n := range []string{"tools", "ade-hooks"} {
		if c := checks[n]; !c.OK || c.Detail != "skipped: not inside a git worktree" {
			t.Fatalf("%s = %+v (order %v)", n, c, order)
		}
	}
}

func TestDoctorResolveErrorInsideRepoIsGeneric(t *testing.T) {
	stubProbe(t)
	_, getenv, state := domainFixture(t, "")
	// git says it is inside a worktree but cannot resolve it.
	testutil.StubTool(t, filepath.Join(state, "bin"), "git",
		"#!/bin/bash\ncase \"$*\" in *--is-inside-work-tree*) echo true; exit 0;; esac\necho boom >&2\nexit 1\n")
	_, checks, _ := doctorChecks(t, getenv)
	if c := checks["tools"]; c.OK || c.Warn || !strings.Contains(c.Detail, "boom") {
		t.Fatalf("tools = %+v", c)
	}
	if c := checks["ade-hooks"]; c.OK || !c.Warn || !strings.Contains(c.Detail, "boom") {
		t.Fatalf("ade-hooks = %+v", c)
	}
}
