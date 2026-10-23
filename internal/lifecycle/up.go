package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/quotedo/hades/internal/caddy"
	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/envfile"
	"github.com/quotedo/hades/internal/execx"
	"github.com/quotedo/hades/internal/mcp"
	"github.com/quotedo/hades/internal/naming"
	"github.com/quotedo/hades/internal/preset"
	"github.com/quotedo/hades/internal/registry"
	"github.com/quotedo/hades/internal/wtctx"
)

// UpOptions are the flags of `hades up`.
type UpOptions struct {
	DryRun, SkipSetup bool
}

// Outcome is what Up set up (or, in a dry run, would have set up).
type Outcome struct {
	Manifest *registry.Manifest
	Env      []envfile.KV
	Steps    []string
}

// ErrLabelTaken reports a label whose manifest belongs to another user or
// another worktree.
type ErrLabelTaken struct {
	Label, User, Worktree string
}

func (e *ErrLabelTaken) Error() string {
	return fmt.Sprintf("label %s taken by %s for %s", e.Label, e.User, e.Worktree)
}

// SetupError reports a failed project setup command. Up keeps everything it
// set up when this happens, so the user can fix the cause and re-run.
type SetupError struct {
	Command string
	Err     error
}

func (e *SetupError) Error() string { return fmt.Sprintf("setup %q: %v", e.Command, e.Err) }

func (e *SetupError) Unwrap() error { return e.Err }

const gitSecretRemedy = "this machine's GPG key must be added by a current key holder: `git secret tell <email>`"

// undo reverses one thing the current run created.
type undo struct {
	action string // what fn does, e.g. "release port 4100"
	fn     func() error
}

// upRun carries the state of one Up call from step to step.
type upRun struct {
	ctx context.Context
	d   Deps
	c   wtctx.Context
	opt UpOptions

	cfg        *config.Project
	preset     preset.Preset
	project    string
	label      string
	noRegistry bool               // dry run on a laptop whose registry does not exist yet
	existing   *registry.Manifest // this worktree's manifest from an earlier up
	m          *registry.Manifest
	env        []envfile.KV
	steps      []string
}

// Up brings the worktree's environment up (spec 6.4 steps 1 to 10). A failure
// in steps 1 to 9 undoes what this run created; a failure in the project's
// setup commands (step 10) is a *SetupError and keeps everything.
func Up(ctx context.Context, d Deps, c wtctx.Context, opt UpOptions) (*Outcome, error) {
	if d.Log == nil {
		d.Log = io.Discard
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if opt.DryRun {
		d.Run = &execx.DryRun{Out: d.Log, Next: d.Run}
	}
	u := &upRun{ctx: ctx, d: d, c: c, opt: opt}

	if err := u.detect(); err != nil {
		return nil, err
	}
	if err := u.prepareRegistry(); err != nil {
		return nil, err
	}
	// The label is checked even for a worktree with nothing to set up, so a
	// label held by another worktree is reported rather than ignored.
	if err := u.identify(); err != nil {
		return nil, err
	}
	if u.preset.Kind == preset.None {
		fmt.Fprintln(d.Log, "up: nothing to do (no preset matched)")
		return &Outcome{}, nil
	}
	u.plan()
	if u.existing != nil {
		if err := u.checkPortOwned(u.existing.Port); err != nil {
			return nil, err
		}
	}

	var stack []undo
	for _, step := range []func() (*undo, error){
		u.claimPort,     // 2
		u.writeManifest, // 3
		u.computeEnv,    // 4
		u.writeEnv,      // 5
		u.revealSecrets, // 6
		u.ensureVhost,   // 7
		u.publishSite,   // 8
		u.rewireMCP,     // 9
	} {
		un, err := step()
		if un != nil {
			stack = append(stack, *un)
		}
		if err != nil {
			return nil, rollback(stack, err)
		}
	}

	out := &Outcome{Manifest: u.m, Env: u.env}
	err := u.setup() // 10
	out.Steps = u.steps
	return out, err
}

// rollback runs the undo stack newest first, carrying on past failures, and
// wraps err with what it undid.
func rollback(stack []undo, err error) error {
	if len(stack) == 0 {
		return err
	}
	parts := make([]string, 0, len(stack))
	for i := len(stack) - 1; i >= 0; i-- {
		if uerr := stack[i].fn(); uerr != nil {
			parts = append(parts, fmt.Sprintf("%s failed: %v", stack[i].action, uerr))
		} else {
			parts = append(parts, stack[i].action)
		}
	}
	return fmt.Errorf("rolled back: %s: %w", strings.Join(parts, ", "), err)
}

func (u *upRun) would(format string, args ...any) {
	line := "would " + fmt.Sprintf(format, args...)
	fmt.Fprintln(u.d.Log, line)
	u.steps = append(u.steps, line)
}

func (u *upRun) did(format string, args ...any) {
	u.steps = append(u.steps, fmt.Sprintf(format, args...))
}

// detect is step 1a: project config and preset.
func (u *upRun) detect() error {
	cfg, _, err := config.LoadProject(u.c.Worktree, u.c.Root)
	if err != nil {
		return err
	}
	p, err := preset.Detect(u.c.Worktree)
	if err != nil {
		return fmt.Errorf("up: %s: %w", u.c.Worktree, err)
	}
	if p, err = preset.Apply(p, cfg); err != nil {
		return fmt.Errorf("up: %s: %w", u.c.Worktree, err)
	}
	u.cfg, u.preset = cfg, p
	return nil
}

// prepareRegistry checks the provisioned registry on a server and creates the
// per-user one on a laptop.
func (u *upRun) prepareRegistry() error {
	reg := u.d.Reg
	if u.d.Mode == config.Server {
		return reg.Check()
	}
	if !u.opt.DryRun {
		return reg.EnsureDirs(0o700)
	}
	err := reg.Check()
	if errors.Is(err, registry.ErrRegistryMissing) {
		u.would("create the registry directories %s, %s, %s", reg.PortsDir, reg.SitesDir, reg.ManifestsDir)
		u.noRegistry = true
		return nil
	}
	return err
}

// identify is step 1b: find an earlier manifest of this worktree (identity is
// the path, so its label wins over the current name), or derive the label and
// make sure nobody else holds it.
func (u *upRun) identify() error {
	d, c := u.d, u.c
	if !u.noRegistry {
		m, err := d.Reg.ByWorktree(c.Worktree)
		switch {
		case err == nil:
			if err := u.checkOwned(m); err != nil {
				return err
			}
			u.existing = m
		case !errors.Is(err, registry.ErrNoManifest):
			return err
		}
	}

	u.project = u.cfg.Project
	if u.project == "" {
		slug, err := naming.Slug(filepath.Base(c.Root))
		if err != nil {
			return fmt.Errorf("project name from %s: %w", c.Root, err)
		}
		u.project = slug
	}

	if u.existing != nil {
		u.label = u.existing.Label
		return nil
	}
	label, err := naming.Label(d.Host.LabelTemplate, naming.Vars{Project: u.project, User: d.User.Name, Name: c.Name})
	if err != nil {
		return fmt.Errorf("label for worktree name %q: %w", c.Name, err)
	}
	u.label = label
	if u.noRegistry {
		return nil
	}
	m, err := d.Reg.ByLabel(label)
	switch {
	case err == nil:
		if err := u.checkOwned(m); err != nil {
			return err
		}
		u.existing = m
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	return nil
}

// plan builds the manifest this run writes, except for the port and address.
func (u *upRun) plan() {
	d, c, label := u.d, u.c, u.label
	var prefixes []string
	switch {
	case len(u.cfg.Databases) > 0:
		prefixes = u.cfg.Databases
	case u.preset.AppName != "":
		prefixes = []string{u.preset.AppName}
	}

	vhost := ""
	if u.cfg.Services.RabbitMQ.Vhost {
		if d.Rabbit != nil {
			vhost = label
		} else {
			fmt.Fprintln(d.Log, "up: vhost requested but this host has no rabbitmq; skipping")
		}
	}

	u.m = &registry.Manifest{
		HadesVersion: d.Version,
		CreatedAt:    d.Now(),
		User:         d.User.Name,
		UID:          d.User.UID,
		Project:      u.project,
		Name:         c.Name,
		Label:        label,
		Worktree:     c.Worktree,
		Root:         c.Root,
		Preset:       string(u.preset.Kind),
		DBSuffix:     naming.Suffix(d.User.Name, naming.Sanitize(c.Name)),
		DBPrefixes:   prefixes,
		Vhost:        vhost,
		ADE:          c.ADE,
	}
	if e := u.existing; e != nil {
		// A rename must not orphan the databases created under the old suffix.
		u.m.CreatedAt, u.m.Name, u.m.DBSuffix = e.CreatedAt, e.Name, e.DBSuffix
	}
}

// checkOwned fails unless m's file is owned by this user and m records this
// worktree. Ownership is the file's uid, not the UID recorded inside.
func (u *upRun) checkOwned(m *registry.Manifest) error {
	owner, err := u.d.Reg.ManifestOwner(m.Label)
	if err != nil {
		return fmt.Errorf("manifest %s: %w", m.Label, err)
	}
	if owner != u.d.User.UID || !sameDir(m.Worktree, u.c.Worktree) {
		return &ErrLabelTaken{Label: m.Label, User: m.User, Worktree: m.Worktree}
	}
	return nil
}

// checkPortOwned makes sure the port an earlier up claimed is still ours.
func (u *upRun) checkPortOwned(port int) error {
	owner, err := u.d.Reg.PortOwner(port)
	if err != nil {
		return fmt.Errorf("port %d recorded in manifest %s: %w", port, u.label, err)
	}
	if !sameDir(owner, u.c.Worktree) {
		return fmt.Errorf("port %d recorded in manifest %s is claimed by %s", port, u.label, owner)
	}
	return nil
}

// sameDir compares two paths after resolving symlinks where they exist.
func sameDir(a, b string) bool {
	canon := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return canon(a) == canon(b)
}

// setAddress fills the host name and URL once the port is known.
func (u *upRun) setAddress(port int) {
	u.m.Port = port
	if u.d.Mode == config.Server {
		u.m.Host = u.m.Label + "." + u.d.Host.Domain
		return
	}
	u.m.Host = "localhost"
}

func (u *upRun) url() string {
	if u.d.Mode == config.Server {
		return "https://" + u.m.Host
	}
	return "http://localhost:" + strconv.Itoa(u.m.Port)
}

// claimPort is step 2.
func (u *upRun) claimPort() (*undo, error) {
	if u.existing != nil {
		u.setAddress(u.existing.Port)
		u.did("reused port %d", u.existing.Port)
		return nil, nil
	}
	reg, wt := u.d.Reg, u.c.Worktree
	if u.opt.DryRun {
		port, err := u.freePort()
		if err != nil {
			return nil, err
		}
		u.setAddress(port)
		u.would("claim port %d", port)
		return nil, nil
	}
	port, err := reg.ClaimPort(wt)
	if err != nil {
		return nil, fmt.Errorf("claim port: %w", err)
	}
	u.setAddress(port)
	u.did("claimed port %d", port)
	return &undo{
		action: fmt.Sprintf("release port %d", port),
		fn:     func() error { return reg.ReleasePort(port, wt) },
	}, nil
}

// freePort returns the port ClaimPort would take, without claiming it.
func (u *upRun) freePort() (int, error) {
	r := u.d.Reg.Range
	for p := r.Lo; p <= r.Hi; p++ {
		if _, err := u.d.Reg.PortOwner(p); errors.Is(err, fs.ErrNotExist) {
			return p, nil
		}
	}
	return 0, fmt.Errorf("claim port: %w", registry.ErrNoFreePort)
}

// writeManifest is step 3. On a re-run the undo restores the earlier manifest.
func (u *upRun) writeManifest() (*undo, error) {
	reg := u.d.Reg
	if u.opt.DryRun {
		u.would("write manifest %s", filepath.Join(reg.ManifestsDir, u.m.Label+".toml"))
		return nil, nil
	}
	if err := reg.WriteManifest(u.m); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}
	u.did("wrote manifest %s", u.m.Label)
	if prev := u.existing; prev != nil {
		return &undo{
			action: "restore manifest " + prev.Label,
			fn:     func() error { return reg.WriteManifest(prev) },
		}, nil
	}
	label := u.m.Label
	return &undo{
		action: "remove manifest " + label,
		fn:     func() error { return reg.RemoveManifest(label) },
	}, nil
}

func (u *upRun) envWorktreePath() string { return filepath.Join(u.c.Worktree, ".env.worktree") }

// computeEnv is step 4. SECRET_KEY_BASE survives re-runs.
func (u *upRun) computeEnv() (*undo, error) {
	prev, err := envfile.ReadEnvWorktree(u.envWorktreePath())
	if err != nil {
		return nil, err
	}
	env, err := envfile.Compute(envfile.Inputs{
		Port:           u.m.Port,
		Label:          u.m.Label,
		Host:           u.m.Host,
		URL:            u.url(),
		Suffix:         u.m.DBSuffix,
		Name:           u.m.Name,
		Project:        u.m.Project,
		User:           u.d.User.Name,
		Preset:         u.preset,
		HostCfg:        *u.d.Host,
		Vhost:          u.m.Vhost,
		Extra:          u.cfg.Env,
		ExistingSecret: prev["SECRET_KEY_BASE"],
	})
	if err != nil {
		return nil, fmt.Errorf("compute environment: %w", err)
	}
	u.env = env
	return nil, nil
}

// writeEnv is step 5. A failing `direnv allow` is reported, not fatal.
func (u *upRun) writeEnv() (*undo, error) {
	wt := u.c.Worktree
	envrc := filepath.Join(wt, ".envrc")
	if u.opt.DryRun {
		u.would("write %s and the hades block of %s", u.envWorktreePath(), envrc)
	} else {
		if err := envfile.WriteEnvWorktree(u.envWorktreePath(), u.env); err != nil {
			return nil, fmt.Errorf("write .env.worktree: %w", err)
		}
		if err := envfile.UpdateEnvrc(envrc, u.env); err != nil {
			return nil, fmt.Errorf("update .envrc: %w", err)
		}
		u.did("wrote .env.worktree and .envrc")
	}
	if _, err := u.d.lookPath("direnv"); err != nil {
		fmt.Fprintln(u.d.Log, "up: direnv not installed; skipping direnv allow")
		return nil, nil
	}
	if _, err := u.d.Run.Run(u.ctx, execx.Cmd{Argv: []string{"direnv", "allow", wt}, Dir: wt}); err != nil {
		fmt.Fprintf(u.d.Log, "up: direnv allow failed (continuing): %v\n", err)
		return nil, nil
	}
	u.did("ran direnv allow")
	return nil, nil
}

// revealSecrets is step 6.
func (u *upRun) revealSecrets() (*undo, error) {
	wt := u.c.Worktree
	if _, err := os.Stat(filepath.Join(wt, ".gitsecret")); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if _, err := u.d.Run.Run(u.ctx, execx.Cmd{Argv: []string{"git", "secret", "reveal", "-f"}, Dir: wt}); err != nil {
		return nil, fmt.Errorf("git secret reveal -f: %w; %s", err, gitSecretRemedy)
	}
	u.did("revealed git secrets")
	return nil, nil
}

// ensureVhost is step 7. Only a vhost this run asked for first is deleted on
// rollback; one an earlier up created stays.
func (u *upRun) ensureVhost() (*undo, error) {
	vhost := u.m.Vhost
	if vhost == "" {
		return nil, nil
	}
	if u.opt.DryRun {
		u.would("ensure rabbitmq vhost %s", vhost)
		return nil, nil
	}
	var un *undo
	if u.existing == nil || u.existing.Vhost != vhost {
		rmq, ctx := u.d.Rabbit, context.WithoutCancel(u.ctx)
		un = &undo{
			action: "delete vhost " + vhost,
			fn:     func() error { return rmq.Delete(ctx, vhost) },
		}
	}
	if err := u.d.Rabbit.Ensure(u.ctx, vhost); err != nil {
		return un, fmt.Errorf("ensure vhost %s: %w", vhost, err)
	}
	u.did("ensured vhost %s", vhost)
	return un, nil
}

// publishSite is step 8: server mode and a web preset only. Only a site file
// this run created is removed on rollback.
func (u *upRun) publishSite() (*undo, error) {
	if u.d.Mode != config.Server || !u.preset.Web {
		return nil, nil
	}
	sites, label := u.d.Reg.SitesDir, u.m.Label
	path := caddy.SitePath(sites, label)
	if u.opt.DryRun {
		u.would("write site %s", path)
		return nil, caddy.SudoReload(u.ctx, u.d.Run, u.d.Bin)
	}
	_, statErr := os.Stat(path)
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return nil, statErr
	}
	if err := caddy.WriteSite(sites, label, u.m.Host, u.m.Port); err != nil {
		return nil, err
	}
	var un *undo
	if statErr != nil {
		run, bin, ctx := u.d.Run, u.d.Bin, context.WithoutCancel(u.ctx)
		un = &undo{
			action: "remove site " + label + " and reload caddy",
			fn: func() error {
				if err := caddy.RemoveSite(sites, label); err != nil {
					return err
				}
				return caddy.SudoReload(ctx, run, bin)
			},
		}
	}
	if err := caddy.SudoReload(u.ctx, u.d.Run, u.d.Bin); err != nil {
		return un, err
	}
	u.did("published %s", u.url())
	return un, nil
}

// rewireMCP is step 9.
func (u *upRun) rewireMCP() (*undo, error) {
	t := mcp.Targets{ChromePath: u.d.Host.ChromePath, Log: u.d.Log}
	if u.preset.Tidewave {
		t.TidewaveURL = fmt.Sprintf("http://localhost:%d/tidewave/mcp", u.m.Port)
	}
	if t.TidewaveURL == "" && t.ChromePath == "" {
		return nil, nil
	}
	if u.opt.DryRun {
		u.would("rewire agent MCP configs in %s (tidewave %q, chrome %q)", u.c.Worktree, t.TidewaveURL, t.ChromePath)
		return nil, nil
	}
	edited, err := mcp.Rewire(u.ctx, u.c.Worktree, t, u.d.Run)
	if err != nil {
		return nil, err
	}
	if len(edited) > 0 {
		u.did("rewired %s", strings.Join(edited, ", "))
	}
	return nil, nil
}

// setup is step 10: the project's own setup commands, from the worktree, with
// the process environment overlaid by the computed one.
func (u *upRun) setup() error {
	if u.opt.SkipSetup {
		fmt.Fprintln(u.d.Log, "up: skipping setup (--skip-setup)")
		return nil
	}
	env := mergeEnv(os.Environ(), u.env)
	for _, command := range u.preset.Setup {
		if !u.opt.DryRun {
			fmt.Fprintf(u.d.Log, "up: setup: %s\n", command)
		}
		res, err := u.d.Run.Run(u.ctx, execx.Cmd{Argv: []string{"sh", "-c", command}, Dir: u.c.Worktree, Env: env})
		io.WriteString(u.d.Log, res.Stdout)
		if err != nil {
			// The error already carries stderr.
			return &SetupError{Command: command, Err: err}
		}
		io.WriteString(u.d.Log, res.Stderr)
		u.did("ran %s", command)
	}
	return nil
}

// mergeEnv returns base with every key of kv replaced by kv's value.
func mergeEnv(base []string, kv []envfile.KV) []string {
	set := make(map[string]bool, len(kv))
	for _, e := range kv {
		set[e.Key] = true
	}
	out := make([]string, 0, len(base)+len(kv))
	for _, e := range base {
		if k, _, _ := strings.Cut(e, "="); !set[k] {
			out = append(out, e)
		}
	}
	for _, e := range kv {
		out = append(out, e.Key+"="+e.Value)
	}
	return out
}
