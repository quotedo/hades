package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/quotedo/hades/internal/naming"
	"github.com/quotedo/hades/internal/registry"
	"github.com/quotedo/hades/internal/testutil"
)

// e2e is a host with domain example.test in temp directories, with the
// phoenix-project fixture committed in a worktree on branch feature/e2e of a
// repository named demo. The process environment (PATH, HOME,
// HADES_HOST_CONFIG, HADES_DB_PORT) points at it, so the commands hades runs
// see it too.
type e2e struct {
	t              *testing.T
	reg            *registry.Registry
	root, worktree string
	label          string // <user>-demo-feature-e2e
	bin            string // the stub tools; all of PATH besides /usr/bin and /bin
	log            string // STUB_LOG: what the stub mix and caddy recorded
	stdout, stderr string // of the last run
}

const (
	stubExit0   = "#!/bin/bash\nexit 0\n"
	caddyReload = "caddy reload --config /etc/caddy/Caddyfile"
)

func newE2E(t *testing.T) *e2e {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("e2e needs git: %v", err)
	}
	reg, getenv, state := domainFixture(t, "")
	root, wt := testutil.TempGitRepo(t, "demo", "feature/e2e")
	testutil.CopyDir(t, filepath.Join("testdata", "phoenix-project"), wt)
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.name=hades-test", "-c", "user.email=hades@test", "-c", "commit.gpgsign=false", "commit", "-m", "fixture"},
	} {
		if out, err := exec.Command(realGit, append([]string{"-C", wt}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	label, err := naming.Label(naming.DomainLabelTemplate, naming.Vars{Project: "demo", User: currentUserName(t), Name: "feature/e2e"})
	if err != nil {
		t.Fatal(err)
	}
	h := &e2e{
		t: t, reg: reg, root: root, worktree: wt, label: label,
		bin: filepath.Join(state, "bin"),
		log: filepath.Join(state, "stub.log"),
	}
	for name, script := range map[string]string{
		"git":    "#!/bin/bash\nexec '" + realGit + "' \"$@\"\n",
		"mix":    "#!/bin/bash\n{ echo \"mix $*\"; echo \"cwd=$(pwd -P)\"; env; } >> \"$STUB_LOG\"\n",
		"direnv": stubExit0,
		"caddy":  "#!/bin/bash\necho \"caddy $*\" >> \"$STUB_LOG\"\n",
	} {
		testutil.StubTool(t, h.bin, name, script)
	}
	// Only the stubs and the base system: no developer's mix, direnv or caddy.
	t.Setenv("PATH", h.bin+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
	t.Setenv("HOME", state)
	t.Setenv("HADES_HOST_CONFIG", getenv("HADES_HOST_CONFIG"))
	t.Setenv("HADES_DB_PORT", getenv("HADES_DB_PORT"))
	t.Setenv("STUB_LOG", h.log)
	// An ADE that runs these tests must not leak its worktree into them.
	for _, k := range []string{
		"PASEO_SOURCE_CHECKOUT_PATH", "PASEO_ROOT_PATH", "PASEO_WORKTREE_PATH", "PASEO_BRANCH_NAME",
		"ORCA_ROOT_PATH", "ORCA_WORKTREE_PATH", "ORCA_WORKSPACE_NAME",
	} {
		t.Setenv(k, "")
	}
	return h
}

// run runs hades in-process with the process environment and returns the
// exit code; stdout and stderr are kept on h.
func (h *e2e) run(args ...string) int {
	var out, errb bytes.Buffer
	code := Main(args, &out, &errb, os.Getenv)
	h.stdout, h.stderr = out.String(), errb.String()
	return code
}

func (h *e2e) manifestByLabel(label string) *registry.Manifest {
	h.t.Helper()
	m, err := h.reg.ByLabel(label)
	if err != nil {
		h.t.Fatalf("manifest %s: %v", label, err)
	}
	return m
}

func (h *e2e) read(path string) string {
	h.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(data)
}

func (h *e2e) stubLog() string { return h.read(h.log) }

// stubLogExcerpt is the stub log without the inherited environment, which
// may hold the developer's or CI's secrets (HADES_TEST_PG, a developer's
// HADES_* variables): the argv and cwd lines and an allow-list of the exact
// variables hades sets that carry no secret.
func (h *e2e) stubLogExcerpt() string {
	var keep []string
	for _, l := range strings.Split(h.stubLog(), "\n") {
		key, _, isVar := strings.Cut(l, "=")
		switch {
		case strings.HasPrefix(l, "mix "), strings.HasPrefix(l, "cwd="):
			keep = append(keep, l)
		case isVar && slices.Contains([]string{"PORT", "PHX_HOST", "HADES_URL", "HADES_LABEL", "DB_HOST", "DB_PORT", "DB_SUFFIX", "DEMO_ENDPOINT"}, key):
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

// stubLogHas reports whether the stub log has exactly this line.
func (h *e2e) stubLogHas(line string) bool {
	return slices.Contains(strings.Split(h.stubLog(), "\n"), line)
}

func (h *e2e) realWorktree() string {
	h.t.Helper()
	p, err := filepath.EvalSymlinks(h.worktree)
	if err != nil {
		h.t.Fatal(err)
	}
	return p
}

func assertGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s still there (stat: %v)", path, err)
	}
}

func TestEndToEndUpThenDown(t *testing.T) {
	h := newE2E(t)
	t.Chdir(h.worktree)

	if code := h.run("up", "--ade", "shell"); code != 0 {
		t.Fatalf("up: code=%d\n%s", code, h.stderr)
	}
	if !strings.Contains(h.stderr, "hades: "+h.label+" up at https://"+h.label+".example.test\n") {
		t.Fatalf("up stderr:\n%s", h.stderr)
	}
	for _, bad := range []string{"skip-worktree", "direnv allow failed", "direnv not installed"} {
		if strings.Contains(h.stderr, bad) {
			t.Fatalf("up stderr has %q:\n%s", bad, h.stderr)
		}
	}
	m := h.manifestByLabel(h.label)
	if m.Port != 4100 || m.Host != h.label+".example.test" || m.ADE != "shell" || m.Name != "feature/e2e" || m.Worktree != h.realWorktree() {
		t.Fatalf("manifest = %+v", m)
	}
	for _, line := range []string{"mix setup", "cwd=" + h.realWorktree(), "PORT=4100", "DEMO_ENDPOINT=" + h.label + ".example.test"} {
		if !h.stubLogHas(line) {
			t.Fatalf("stub log lacks %q:\n%s", line, h.stubLogExcerpt())
		}
	}
	if site := h.read(filepath.Join(h.reg.SitesDir, h.label+".caddy")); site != "import worktree "+h.label+".example.test 4100\n" {
		t.Fatalf("site file = %q", site)
	}
	if !h.stubLogHas(caddyReload) {
		t.Fatalf("stub log lacks %q", caddyReload)
	}
	if mcp := h.read(filepath.Join(h.worktree, ".mcp.json")); !strings.Contains(mcp, `"url": "http://localhost:4100/tidewave/mcp"`) {
		t.Fatalf(".mcp.json:\n%s", mcp)
	}
	if codex := h.read(filepath.Join(h.worktree, ".codex", "config.toml")); !strings.Contains(codex, `url = "http://localhost:4100/tidewave/mcp"`) {
		t.Fatalf(".codex/config.toml:\n%s", codex)
	}

	if code := h.run("ls", "--json"); code != 0 {
		t.Fatalf("ls: code=%d\n%s", code, h.stderr)
	}
	var listed []registry.Manifest
	if err := json.Unmarshal([]byte(h.stdout), &listed); err != nil || len(listed) != 1 || listed[0].Label != h.label {
		t.Fatalf("ls --json: %v: %s", err, h.stdout)
	}
	if code := h.run("port"); code != 0 || h.stdout != "4100\n" {
		t.Fatalf("port: code=%d out=%q err=%q", code, h.stdout, h.stderr)
	}

	if code := h.run("down"); code != 0 {
		t.Fatalf("down: code=%d\n%s", code, h.stderr)
	}
	// There is no Postgres here, so dropping the databases is the one step
	// that may fail; the site and port must go, and the manifest goes too
	// unless that step failed, in which case it stays for gc to retry.
	downErr, dbFailed := h.stderr, false
	for _, line := range strings.Split(downErr, "\n") {
		if strings.HasPrefix(line, "down: failed databases: ") {
			dbFailed = true
		} else if strings.HasPrefix(line, "down: failed ") {
			t.Fatalf("down: %s\n%s", line, h.stderr)
		}
	}
	assertGone(t, filepath.Join(h.reg.SitesDir, h.label+".caddy"))
	assertGone(t, filepath.Join(h.reg.PortsDir, "4100"))
	if n := strings.Count(h.stubLog(), caddyReload+"\n"); n != 2 {
		t.Fatalf("caddy reloaded %d times, want 2 (up and down)", n)
	}
	if code := h.run("ls", "--json"); code != 0 {
		t.Fatalf("ls after down: code=%d err=%q", code, h.stderr)
	}
	listed = nil
	if err := json.Unmarshal([]byte(h.stdout), &listed); err != nil {
		t.Fatalf("ls --json after down: %v: %s", err, h.stdout)
	}
	if dbFailed {
		if !strings.Contains(downErr, "down: manifest kept (failed: databases); gc will retry\n") || len(listed) != 1 || listed[0].Label != h.label {
			t.Fatalf("manifest not kept after a failed step: %s\n%s", h.stdout, downErr)
		}
	} else if len(listed) != 0 {
		t.Fatalf("manifest left after a clean down: %s", h.stdout)
	}
}

func TestEndToEndPaseoEnvAndDoctor(t *testing.T) {
	h := newE2E(t)
	t.Chdir(t.TempDir()) // Paseo names the worktree; the current directory is elsewhere
	t.Setenv("PASEO_SOURCE_CHECKOUT_PATH", h.root)
	t.Setenv("PASEO_WORKTREE_PATH", h.worktree)
	t.Setenv("PASEO_BRANCH_NAME", "feature/e2e")

	if code := h.run("up"); code != 0 {
		t.Fatalf("up: code=%d\n%s", code, h.stderr)
	}
	m := h.manifestByLabel(h.label)
	if m.ADE != "paseo" || m.Name != "feature/e2e" || m.Port != 4100 || m.Worktree != h.realWorktree() {
		t.Fatalf("manifest = %+v", m)
	}
	if !h.stubLogHas("mix setup") || !h.stubLogHas("cwd="+h.realWorktree()) {
		t.Fatalf("stub log:\n%s", h.stubLogExcerpt())
	}

	stubProbe(t)
	code := h.run("doctor")
	if code != 0 || strings.Contains(h.stdout, "FAIL") {
		t.Fatalf("doctor: code=%d\n%s%s", code, h.stdout, h.stderr)
	}
	// Each check really ran against the stubs and the Paseo worktree.
	for _, want := range []string{
		"ok   host-config: loaded " + os.Getenv("HADES_HOST_CONFIG") + " (domain example.test)\n",
		"ok   postgres: reachable, 2 databases\n",
		"ok   git: " + filepath.Join(h.bin, "git") + "\n",
		"ok   tools: " + filepath.Join(h.bin, "mix") + "\n",
		"ok   direnv: found\n",
		"ok   ade-hooks: paseo.json run hades up\n",
		"ok   versions:",
	} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("doctor output lacks %q:\n%s", want, h.stdout)
		}
	}
}
