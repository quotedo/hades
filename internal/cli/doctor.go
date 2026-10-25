package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/quotedo/hades/internal/caddy"
	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/execx"
	"github.com/quotedo/hades/internal/lifecycle"
	"github.com/quotedo/hades/internal/preset"
	"github.com/quotedo/hades/internal/version"
	"github.com/quotedo/hades/internal/wtctx"
)

// check is one line of the doctor report. A failed check with Warn set is
// advice and does not change the exit code.
type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Warn   bool   `json:"warn"`
	Detail string `json:"detail"`
}

func pass(name, detail string) check { return check{Name: name, OK: true, Detail: detail} }
func fail(name string, err error) check {
	return check{Name: name, Detail: err.Error()}
}
func failf(name, format string, a ...any) check {
	return check{Name: name, Detail: fmt.Sprintf(format, a...)}
}

// warnIf is a warn-only check: ok when problem is empty.
func warnIf(name, problem, okDetail string) check {
	if problem == "" {
		return pass(name, okDetail)
	}
	return check{Name: name, Warn: true, Detail: problem}
}

// doctor verifies that this machine can run hades and reports every check.
// It exits 1 when a check that is not warn-only fails.
func doctor(ctx *Context, args []string) int {
	if !parseArgs(ctx, flag.NewFlagSet("doctor", flag.ContinueOnError), "usage: hades doctor [--json]", args) {
		return 2
	}
	checks := runChecks(context.Background(), ctx)

	code := 0
	for _, c := range checks {
		if !c.OK && !c.Warn {
			code = 1
		}
	}
	if ctx.Flags.JSON {
		if err := writeJSON(ctx.Stdout, checks); err != nil {
			return ctx.fail("doctor", err)
		}
		return code
	}
	for _, c := range checks {
		label := "ok  "
		switch {
		case c.OK:
		case c.Warn:
			label = "WARN"
		default:
			label = "FAIL"
		}
		fmt.Fprintf(ctx.Stdout, "%s %s: %s\n", label, c.Name, c.Detail)
	}
	return code
}

func runChecks(bg context.Context, ctx *Context) []check {
	d, err := newDeps(ctx)
	if err != nil {
		// Nothing else can run without the host config.
		return []check{fail("host-config", err)}
	}
	checks := []check{pass("host-config", "loaded in "+modeName(d.Mode)+" mode")}

	regCheck := checkRegistry(d)
	checks = append(checks, regCheck)
	switch {
	case d.Mode != config.Server:
	case !regCheck.OK:
		checks = append(checks, failf("registry-perms", "not checked: registry directories are missing"))
	default:
		checks = append(checks, checkRegistryPerms(d))
	}
	if d.Mode == config.Server {
		if err := caddy.SudoCheck(bg, d.Run, d.Bin); err != nil {
			checks = append(checks, fail("caddy-reload", err))
		} else {
			checks = append(checks, pass("caddy-reload", "sudo -n "+d.Bin+" caddy-reload --check succeeded"))
		}
	}

	pgCtx, cancel := context.WithTimeout(bg, probeTimeout)
	defer cancel()
	if dbs, err := pgProbe(pgCtx, d.PG); err != nil {
		checks = append(checks, fail("postgres", err))
	} else {
		checks = append(checks, pass("postgres", fmt.Sprintf("reachable, %d databases", len(dbs))))
	}

	if r := d.Host.RabbitMQ; r != nil {
		checks = append(checks, checkRabbit(bg, r))
	}

	lookup := toolLookup(d, ctx.Getenv("PATH"))
	if p, err := lookup("git"); err != nil {
		checks = append(checks, fail("git", err))
	} else {
		checks = append(checks, pass("git", p))
	}

	wt, wtErr, inRepo := repoWorktree(bg, ctx, d)
	switch {
	case !inRepo:
		checks = append(checks, pass("tools", skipNoRepo))
	case wtErr == nil:
		checks = append(checks, checkTools(wt.Worktree, lookup))
	default:
		checks = append(checks, fail("tools", wtErr))
	}

	_, derr := lookup("direnv")
	problem := ""
	if derr != nil {
		problem = derr.Error()
	}
	checks = append(checks, warnIf("direnv", problem, "found"))

	switch {
	case !inRepo:
		checks = append(checks, pass("ade-hooks", skipNoRepo))
	case wtErr == nil:
		checks = append(checks, checkADEHooks(wt.Worktree))
	default:
		checks = append(checks, check{Name: "ade-hooks", Warn: true, Detail: wtErr.Error()})
	}
	checks = append(checks, checkManifests(d), checkVersions(d))
	return checks
}

const skipNoRepo = "skipped: not inside a git worktree"

// repoWorktree resolves the worktree the repo checks look at: the one the
// flags or the ADE environment name, else the current one when the working
// directory is inside a git worktree. git's own exit status decides that, not
// its message (which is localized); inRepo is false only when git ran and
// said no. A git that cannot start, or a resolution failure, is returned as
// err.
func repoWorktree(bg context.Context, ctx *Context, d lifecycle.Deps) (wt wtctx.Context, err error, inRepo bool) {
	if wtctx.NamesWorktree(ctx.Flags.Flags, ctx.Getenv) {
		wt, err = resolveWorktree(bg, ctx)
		return wt, err, true
	}
	cwd, err := os.Getwd()
	if err != nil {
		return wt, err, true
	}
	env := append(os.Environ(), "LC_ALL=C", "LANG=C")
	_, err = d.Run.Run(bg, execx.Cmd{Argv: []string{"git", "-C", cwd, "rev-parse", "--is-inside-work-tree"}, Env: env})
	var xe *execx.ExitError
	if errors.As(err, &xe) {
		return wt, nil, false
	}
	if err != nil {
		return wt, err, true
	}
	wt, err = resolveWorktree(bg, ctx)
	return wt, err, true
}

func modeName(m config.Mode) string {
	if m == config.Server {
		return "server"
	}
	return "laptop"
}

func registryDirs(d lifecycle.Deps) []string {
	return []string{d.Reg.PortsDir, d.Reg.SitesDir, d.Reg.ManifestsDir}
}

func checkRegistry(d lifecycle.Deps) check {
	if err := d.Reg.Exists(); err != nil {
		return fail("registry", err)
	}
	return pass("registry", strings.Join(registryDirs(d), ", "))
}

// checkRegistryPerms reports what a shared registry directory lacks (setgid,
// sticky, group write, no world write, this process in its group): the
// conditions the server-mode registry check refuses to start without.
func checkRegistryPerms(d lifecycle.Deps) check {
	const name = "registry-perms"
	problems, err := d.Reg.PermProblems()
	if err != nil {
		return fail(name, err)
	}
	if len(problems) > 0 {
		return failf(name, "%s", strings.Join(problems, "; "))
	}
	return pass(name, "setgid, sticky, group-writable, not world-writable, this process in the group")
}

func checkRabbit(bg context.Context, r *config.RabbitMQ) check {
	const name = "rabbitmq"
	ctx, cancel := context.WithTimeout(bg, probeTimeout)
	defer cancel()
	url := strings.TrimRight(r.ManagementURL, "/") + "/api/overview"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fail(name, err)
	}
	req.SetBasicAuth(r.User, r.Password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fail(name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return failf(name, "GET %s: %s", url, resp.Status)
	}
	return pass(name, "management API reachable at "+r.ManagementURL)
}

// toolLookup resolves a tool the way `up` runs commands: the version-manager
// directories first, then PATH.
func toolLookup(d lifecycle.Deps, path string) func(string) (string, error) {
	prepend := execx.ToolPrepend(d.User.Home, d.Host.ToolPaths)
	return func(name string) (string, error) { return execx.LookPath(name, prepend, path) }
}

// checkTools verifies the toolchain of the worktree's preset is installed.
func checkTools(wtDir string, lookup func(string) (string, error)) check {
	const name = "tools"
	p, err := preset.Detect(wtDir)
	if err != nil {
		return fail(name, err)
	}
	var tools []string
	switch p.Kind {
	case preset.Phoenix, preset.Elixir:
		tools = []string{"mix"}
	case preset.Go:
		tools = []string{"go"}
	case preset.Node:
		tools = []string{nodeManager(wtDir)}
	default:
		return pass(name, "no preset tools needed")
	}
	var found []string
	for _, t := range tools {
		path, err := lookup(t)
		if err != nil {
			return failf(name, "%s preset needs %s: %v", p.Kind, t, err)
		}
		found = append(found, path)
	}
	return pass(name, strings.Join(found, ", "))
}

// nodeManager is the package manager of the lockfile, npm without one.
func nodeManager(dir string) string {
	for _, l := range []struct{ file, tool string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}} {
		if _, err := os.Stat(filepath.Join(dir, l.file)); err == nil {
			return l.tool
		}
	}
	return "npm"
}

// checkADEHooks warns when an ADE config exists but never runs `hades up`.
func checkADEHooks(wtDir string) check {
	const name = "ade-hooks"
	var found, missing []string
	for _, f := range []string{"paseo.json", "orca.yaml"} {
		data, err := os.ReadFile(filepath.Join(wtDir, f))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return check{Name: name, Warn: true, Detail: err.Error()}
		}
		found = append(found, f)
		if !strings.Contains(string(data), "hades up") {
			missing = append(missing, f)
		}
	}
	switch {
	case len(found) == 0:
		return pass(name, "no paseo.json or orca.yaml")
	case len(missing) > 0:
		return warnIf(name, strings.Join(missing, ", ")+" does not mention `hades up`", "")
	}
	return pass(name, strings.Join(found, ", ")+" run hades up")
}

// checkManifests fails when the registry holds entries hades ignores: files
// that are not regular, misnamed, or unparsable.
func checkManifests(d lifecycle.Deps) check {
	const name = "manifests"
	all, problems, err := allManifests(d)
	if err != nil {
		return fail(name, err)
	}
	if len(problems) > 0 {
		lines := make([]string, len(problems))
		for i, p := range problems {
			lines[i] = p.String()
		}
		return failf(name, "%s", strings.Join(lines, "; "))
	}
	return pass(name, fmt.Sprintf("%d manifests readable", len(all)))
}

// checkVersions warns about manifests written by another hades version.
func checkVersions(d lifecycle.Deps) check {
	const name = "versions"
	all, _, err := allManifests(d)
	if err != nil {
		return check{Name: name, Warn: true, Detail: err.Error()}
	}
	var stale []string
	for _, m := range all {
		if m.HadesVersion != version.Version {
			stale = append(stale, m.Label)
		}
	}
	if len(stale) > 0 {
		return warnIf(name, fmt.Sprintf("manifests not from hades %s: %s", version.Version, strings.Join(stale, ", ")), "")
	}
	return pass(name, "all manifests from hades "+version.Version)
}
