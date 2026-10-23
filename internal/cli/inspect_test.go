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

	"github.com/quotedo/hades/internal/pg"
	"github.com/quotedo/hades/internal/registry"
	"github.com/quotedo/hades/internal/testutil"
)

// serverFixture writes a server host config under a temp dir and returns the
// registry, a getenv pointing at it (HADES_BIN at a stub that accepts
// `caddy-reload --check`) and the state directory. Postgres points at a closed
// port, so the postgres check fails fast. extra is appended to the host config.
func serverFixture(t *testing.T, extra string) (*registry.Registry, func(string) string, string) {
	t.Helper()
	state := t.TempDir()
	bin := filepath.Join(state, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.StubTool(t, bin, "hades-stub", "#!/bin/bash\nexit 0\n")
	// sudo -n <bin> <args>: drop -n and run the rest.
	testutil.StubTool(t, bin, "sudo", "#!/bin/bash\nshift\nexec \"$@\"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, d := range []string{"ports", "sites", "manifests"} {
		if err := os.Mkdir(filepath.Join(state, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hostFile := filepath.Join(state, "host.toml")
	content := `domain = "example.test"
port_range = "4100-4119"
ports_dir = "` + filepath.Join(state, "ports") + `"
sites_dir = "` + filepath.Join(state, "sites") + `"
manifests_dir = "` + filepath.Join(state, "manifests") + `"
[caddy]
config = "/etc/caddy/Caddyfile"
live_dir = "/etc/caddy/worktrees"
reload = ["caddy", "reload"]
[postgres]
host = "127.0.0.1"
port = 1
user = "postgres"
password = "postgres"
` + extra
	if err := os.WriteFile(hostFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		switch k {
		case "HADES_HOST_CONFIG":
			return hostFile
		case "HADES_BIN":
			return filepath.Join(bin, "hades-stub")
		case "PATH":
			return os.Getenv("PATH")
		case "HOME":
			return state
		}
		return ""
	}
	reg := &registry.Registry{
		PortsDir:     filepath.Join(state, "ports"),
		SitesDir:     filepath.Join(state, "sites"),
		ManifestsDir: filepath.Join(state, "manifests"),
	}
	return reg, getenv, state
}

func runMain(getenv func(string) string, args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = Main(args, &out, &errb, getenv)
	return code, out.String(), errb.String()
}

func TestLsJSONAndMine(t *testing.T) {
	_, _, getenv := laptopUpFixture(t, "phoenix")
	reg := laptopRegistry(t, getenv)
	me := currentUserName(t)

	// An empty registry lists as [], not null.
	code, out, errs := runMain(getenv, "ls", "--json")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty: code=%d out=%q err=%q", code, out, errs)
	}

	for _, m := range []*registry.Manifest{
		{User: me, Label: "demo-a", Port: 4100, Host: "demo-a.example.test", Worktree: "/w/a", CreatedAt: time.Now().Add(-3 * time.Hour)},
		{User: "other", Label: "demo-b", Port: 4101, Host: "demo-b.example.test", Worktree: "/w/b", CreatedAt: time.Now().Add(-49 * time.Hour)},
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
	code, out, _ = runMain(getenv, "ls", "--mine", "--json")
	var mine []registry.Manifest
	if err := json.Unmarshal([]byte(out), &mine); err != nil || code != 0 || len(mine) != 1 || mine[0].User != me {
		t.Fatalf("mine: code=%d err=%v out=%q", code, err, out)
	}

	code, out, _ = runMain(getenv, "ls")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) != 3 || strings.Fields(lines[0])[0] != "LABEL" ||
		!strings.HasSuffix(lines[1], "3h") || !strings.HasSuffix(lines[2], "2d") {
		t.Fatalf("table: code=%d out=%q", code, out)
	}
}

func TestPortFromPaseoEnv(t *testing.T) {
	reg, getenv, state := serverFixture(t, "")
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
	if code != 0 || out != "export NAME='it'\\''s'\nexport PORT='4100'\n" {
		t.Fatalf("shell: code=%d out=%q err=%q", code, out, errs)
	}
	code, out, _ = runMain(getenv, args("env", "--json")...)
	var m map[string]string
	if err := json.Unmarshal([]byte(out), &m); err != nil || code != 0 || m["PORT"] != "4100" || m["NAME"] != "it's" {
		t.Fatalf("json: code=%d err=%v out=%q", code, err, out)
	}
}

func TestDoctorFailsOnMissingRegistryDir(t *testing.T) {
	_, getenv, state := serverFixture(t, "")
	missing := filepath.Join(state, "manifests")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runMain(getenv, "doctor")
	if code != 1 || !strings.Contains(out+errs, missing) || !strings.Contains(out+errs, "FAIL registry") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
}

type doctorCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Warn   bool   `json:"warn"`
	Detail string `json:"detail"`
}

func doctorChecks(t *testing.T, getenv func(string) string) (int, map[string]doctorCheck, []string) {
	t.Helper()
	code, out, errs := runMain(getenv, "doctor", "--json")
	var list []doctorCheck
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("doctor --json: %v: out=%q err=%q", err, out, errs)
	}
	byName := map[string]doctorCheck{}
	var order []string
	for _, c := range list {
		byName[c.Name] = c
		order = append(order, c.Name)
	}
	return code, byName, order
}

func TestDoctorRegistryPermsFailWithoutSetgid(t *testing.T) {
	_, getenv, _ := serverFixture(t, "")
	code, checks, order := doctorChecks(t, getenv)
	if code != 1 {
		t.Fatalf("code=%d", code)
	}
	if c := checks["registry-perms"]; c.OK || c.Warn || !strings.Contains(c.Detail, "setgid") {
		t.Fatalf("registry-perms = %+v", c)
	}
	if !checks["registry"].OK || !checks["host-config"].OK {
		t.Fatalf("checks = %+v", checks)
	}
	if c := checks["caddy-reload"]; !c.OK {
		t.Fatalf("caddy-reload = %+v", c)
	}
	if c := checks["postgres"]; c.OK || c.Warn {
		t.Fatalf("postgres = %+v", c)
	}
	if !checks["direnv"].Warn && checks["direnv"].OK == false {
		t.Fatalf("direnv must be warn-only: %+v", checks["direnv"])
	}
	if order[0] != "host-config" || order[1] != "registry" || order[2] != "registry-perms" || order[3] != "caddy-reload" {
		t.Fatalf("order = %v", order)
	}
}

func TestDoctorRegistryPermsPassWithSetgid(t *testing.T) {
	_, getenv, state := serverFixture(t, "")
	for _, d := range []string{"ports", "sites", "manifests"} {
		p := filepath.Join(state, d)
		if err := os.Chmod(p, 0o775|os.ModeSetgid); err != nil {
			t.Skipf("cannot set setgid here: %v", err)
		}
		if fi, err := os.Stat(p); err != nil || fi.Mode()&os.ModeSetgid == 0 {
			t.Skip("filesystem does not keep the setgid bit")
		}
	}
	_, checks, _ := doctorChecks(t, getenv)
	c := checks["registry-perms"]
	if !c.OK && strings.Contains(c.Detail, "not a member") {
		t.Skipf("test user is not in the temp dir's group: %s", c.Detail)
	}
	if !c.OK {
		t.Fatalf("registry-perms = %+v", c)
	}
}

func TestDoctorRabbitMQ(t *testing.T) {
	var gotUser, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, _, _ = r.BasicAuth()
		gotPath = r.URL.Path
	}))
	defer srv.Close()
	_, getenv, _ := serverFixture(t, "[rabbitmq]\nmanagement_url = \""+srv.URL+"\"\namqp_url = \"amqp://x\"\nuser = \"guest\"\npassword = \"guest\"\n")
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
	reg, getenv, _ := serverFixture(t, "")
	if err := reg.WriteManifest(&registry.Manifest{User: "u", Label: "demo-old", HadesVersion: "0.0.1-ancient", Worktree: "/w/old"}); err != nil {
		t.Fatal(err)
	}
	_, checks, _ := doctorChecks(t, getenv)
	if c := checks["versions"]; c.OK || !c.Warn || !strings.Contains(c.Detail, "demo-old") {
		t.Fatalf("versions = %+v", c)
	}
}

func TestStatusJSONShape(t *testing.T) {
	reg, getenv, state := serverFixture(t, "")
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
		Mode       string `json:"mode"`
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
		CaddyCheck string `json:"caddy_check"`
	}
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if s.Mode != "server" || s.HostConfig != getenv("HADES_HOST_CONFIG") || s.Domain != "example.test" ||
		s.Ports.Range != "4100-4119" || s.Ports.Used != 1 || s.Ports.Free != 19 || s.Worktrees != 1 ||
		s.Postgres.Reachable || s.CaddyCheck != "ok" {
		t.Fatalf("status = %+v", s)
	}

	code, out, _ = runMain(getenv, "status")
	if code != 0 || !strings.Contains(out, "mode: server\n") || !strings.Contains(out, "ports.used: "+strconv.Itoa(1)+"\n") {
		t.Fatalf("text: code=%d out=%q", code, out)
	}
}

func TestStatusOnLaptop(t *testing.T) {
	_, _, getenv := laptopUpFixture(t, "phoenix")
	code, out, errs := runMain(getenv, "status", "--json")
	if code != 0 || !strings.Contains(out, `"mode": "laptop"`) || !strings.Contains(out, `"host_config": "laptop"`) || !strings.Contains(out, `"caddy_check": "n/a"`) {
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

// setgidRegistry gives the registry directories the bits doctor demands, or
// skips the test where the filesystem or group membership does not allow it.
func setgidRegistry(t *testing.T, state string, getenv func(string) string) {
	t.Helper()
	for _, d := range []string{"ports", "sites", "manifests"} {
		p := filepath.Join(state, d)
		if err := os.Chmod(p, 0o775|os.ModeSetgid); err != nil {
			t.Skipf("cannot set setgid here: %v", err)
		}
		if fi, err := os.Stat(p); err != nil || fi.Mode()&os.ModeSetgid == 0 {
			t.Skip("filesystem does not keep the setgid bit")
		}
	}
	if _, checks, _ := doctorChecks(t, getenv); !checks["registry-perms"].OK {
		t.Skipf("registry-perms cannot pass here: %s", checks["registry-perms"].Detail)
	}
}

// phoenixDoctorFixture is a server host whose current worktree is a phoenix
// project. PATH holds only the stub dir (with git, and mix when withMix, in
// <home>/.asdf/shims), so direnv is absent.
func phoenixDoctorFixture(t *testing.T, withMix bool) (getenv func(string) string, reg *registry.Registry, wt, shims string) {
	t.Helper()
	stubProbe(t)
	var state string
	reg, getenv, state = serverFixture(t, "")
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
	setgidRegistry(t, state, getenv)
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

func TestDoctorToolsFailWhenMixMissing(t *testing.T) {
	getenv, _, wt, _ := phoenixDoctorFixture(t, false)
	code, out, errs := runMain(getenv, "--root", filepath.Dir(wt), "--worktree", wt, "--name", "feature/x", "doctor")
	if code != 1 || !strings.Contains(out, "FAIL tools: phoenix preset needs mix") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
}

func TestDoctorGitMissingFails(t *testing.T) {
	stubProbe(t)
	_, getenv, _ := serverFixture(t, "")
	getenv = withEnv(getenv, map[string]string{"PATH": t.TempDir()})
	code, checks, _ := doctorChecks(t, getenv)
	if code != 1 || checks["git"].OK || checks["git"].Warn {
		t.Fatalf("code=%d git=%+v", code, checks["git"])
	}
}

func TestDoctorOutsideRepoSkipsRepoChecks(t *testing.T) {
	stubProbe(t)
	_, getenv, _ := serverFixture(t, "")
	t.Chdir(t.TempDir())
	_, checks, order := doctorChecks(t, getenv)
	for _, n := range []string{"tools", "ade-hooks"} {
		if c := checks[n]; !c.OK || c.Detail != "skipped: not inside a git worktree" {
			t.Fatalf("%s = %+v (order %v)", n, c, order)
		}
	}
}
