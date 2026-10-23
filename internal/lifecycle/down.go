package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/quotedo/hades/internal/caddy"
	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/envfile"
	"github.com/quotedo/hades/internal/execx"
	"github.com/quotedo/hades/internal/naming"
	"github.com/quotedo/hades/internal/pg"
	"github.com/quotedo/hades/internal/registry"
	"github.com/quotedo/hades/internal/wtctx"
)

// StepResult is the outcome of one step of Down, or of releasing one manifest
// in GC. A step that had nothing to do produces no result.
type StepResult struct {
	Step    string
	Skipped bool
	Reason  string // why the step was skipped
	Err     error
}

// Down releases everything the worktree's manifest records: it runs the
// project's teardown commands (only as the manifest's owner, not as root),
// drops the databases, deletes the vhost, removes the site and releases the
// port. The steps run whether or not the earlier ones succeeded. The manifest
// is removed only when every step that ran succeeded; otherwise it is kept,
// so ls still shows the leak and gc retries it, and Down says so. Every skip
// and failure is a StepResult and a line on d.Log. Down returns an error only
// when strict is set and a step failed.
func Down(ctx context.Context, d Deps, c wtctx.Context, strict bool) ([]StepResult, error) {
	if d.Log == nil {
		d.Log = io.Discard
	}
	m, owner, guard := ownManifest(d, c)
	if m == nil {
		res := []StepResult{guard}
		logResults(d.Log, "down", res)
		return res, failure(res, strict)
	}
	var res []StepResult
	if owner {
		if r := teardown(ctx, d, c); r != nil {
			res = append(res, *r)
		}
	} else {
		res = append(res, StepResult{Step: "teardown", Skipped: true, Reason: "teardown runs only as the owner; use gc"})
	}
	logResults(d.Log, "down", res)
	rel, _, kept := release(ctx, d, m, true, failedSteps(res))
	logResults(d.Log, "down", rel)
	res = append(res, rel...)
	if len(kept) > 0 {
		reason := fmt.Sprintf("kept (failed: %s); gc will retry", strings.Join(kept, ", "))
		fmt.Fprintf(d.Log, "down: manifest %s\n", reason)
		res = append(res, StepResult{Step: "manifest", Skipped: true, Reason: reason})
	}
	return res, failure(res, strict)
}

// failedSteps names the steps of res that failed.
func failedSteps(res []StepResult) []string {
	var failed []string
	for _, r := range res {
		if r.Err != nil {
			failed = append(failed, r.Step)
		}
	}
	return failed
}

// ownManifest returns the worktree's manifest when this user may release it,
// else nil and the result saying why not. Ownership is the manifest file's
// uid and only this user's manifests are looked up; root sees every
// manifest, and one it does not own is released only after its claims are
// verified against its file owner. owner reports whether this user owns the
// manifest rather than acting as root.
func ownManifest(d Deps, c wtctx.Context) (m *registry.Manifest, owner bool, r StepResult) {
	r = StepResult{Step: "manifest"}
	if d.Mode == config.Laptop {
		if err := d.Reg.Check(); errors.Is(err, registry.ErrRegistryMissing) {
			r.Skipped, r.Reason = true, fmt.Sprintf("no manifest for worktree %s (%v)", c.Worktree, err)
			return nil, false, r
		}
	}
	m, err := d.Reg.ByWorktree(c.Worktree, d.User.UID)
	if errors.Is(err, registry.ErrNoManifest) {
		r.Skipped, r.Reason = true, err.Error()
		if p, ok := ignoredManifestFor(d, c); ok {
			r.Reason += "; " + p.String()
		}
		return nil, false, r
	}
	if err != nil {
		r.Err = err
		return nil, false, r
	}
	uid := m.OwnerUID()
	switch {
	case d.User.UID != 0 && uid != d.User.UID:
		r.Skipped, r.Reason = true, fmt.Sprintf("manifest %s is owned by uid %d, not uid %d", m.Label, uid, d.User.UID)
	case !sameDir(m.Worktree, c.Worktree):
		r.Skipped, r.Reason = true, fmt.Sprintf("manifest %s records worktree %s, not %s", m.Label, m.Worktree, c.Worktree)
	case uid != d.User.UID:
		if reason := verifyClaims(d, m); reason != "" {
			r.Err = fmt.Errorf("refusing %s: %s", m.Path(), reason)
			return nil, false, r
		}
		return m, false, r
	default:
		return m, true, r
	}
	return nil, false, r
}

// ignoredManifestFor finds a manifests directory entry the registry ignored
// that is probably this worktree's: its file is named for the label up would
// derive, or it mentions the worktree path. It only improves down's message.
func ignoredManifestFor(d Deps, c wtctx.Context) (registry.Problem, bool) {
	_, problems, err := d.Reg.All()
	if err != nil || len(problems) == 0 {
		return registry.Problem{}, false
	}
	label := ""
	if cfg, _, err := config.LoadProject(c.Worktree, c.Root); err == nil {
		if project, err := projectSlug(cfg, c.Root); err == nil {
			label, _ = naming.Label(d.Host.LabelTemplate, naming.Vars{Project: project, User: d.User.Name, Name: c.Name})
		}
	}
	quoted := strconv.Quote(c.Worktree)
	for _, p := range problems {
		if label != "" && filepath.Base(p.Path) == label+".toml" {
			return p, true
		}
		if mentionsPath(p.Path, quoted) {
			return p, true
		}
	}
	return registry.Problem{}, false
}

// mentionsPath reports whether the regular file at path contains needle in
// its first 64 KiB. Symlinks are not followed.
func mentionsPath(path, needle string) bool {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64<<10))
	return err == nil && strings.Contains(string(data), needle)
}

// teardown runs the project's teardown commands from the worktree, with the
// process environment overlaid by .env.worktree. A failing command does not
// stop the ones after it.
func teardown(ctx context.Context, d Deps, c wtctx.Context) *StepResult {
	r := &StepResult{Step: "teardown"}
	if _, err := os.Stat(c.Worktree); errors.Is(err, fs.ErrNotExist) {
		r.Skipped, r.Reason = true, fmt.Sprintf("worktree %s no longer exists; teardown commands not run", c.Worktree)
		return r
	} else if err != nil {
		r.Err = err
		return r
	}
	cfg, _, err := config.LoadProject(c.Worktree, c.Root)
	if err != nil {
		r.Err = err
		return r
	}
	if len(cfg.Teardown) == 0 {
		return nil
	}
	vals, err := envfile.ReadEnvWorktree(filepath.Join(c.Worktree, ".env.worktree"))
	if err != nil {
		r.Err = err
		return r
	}
	kv := make([]envfile.KV, 0, len(vals))
	for k, v := range vals {
		kv = append(kv, envfile.KV{Key: k, Value: v})
	}
	sort.Slice(kv, func(i, j int) bool { return kv[i].Key < kv[j].Key })
	env := mergeEnv(os.Environ(), kv)

	var errs []error
	for _, command := range cfg.Teardown {
		fmt.Fprintf(d.Log, "down: teardown: %s\n", command)
		res, err := d.Run.Run(ctx, execx.Cmd{Argv: []string{"sh", "-c", command}, Dir: c.Worktree, Env: env})
		io.WriteString(d.Log, res.Stdout)
		if err != nil {
			// The error already carries stderr.
			errs = append(errs, fmt.Errorf("%q: %w", command, err))
			continue
		}
		io.WriteString(d.Log, res.Stderr)
	}
	r.Err = errors.Join(errs...)
	return r
}

// release undoes what m records: databases, vhost, site, port, and then the
// manifest, but only when none of these steps and none of the steps before
// it (failed) failed. Otherwise the manifest stays as the record of what is
// left, rewritten without the claims this run released (see keepManifest),
// and kept names the failed steps. With reload set, removing a site also
// reloads Caddy; GC passes false and reloads once at the end. siteRemoved
// reports whether a site file went.
func release(ctx context.Context, d Deps, m *registry.Manifest, reload bool, failed []string) (res []StepResult, siteRemoved bool, kept []string) {
	add := func(r *StepResult) {
		if r != nil {
			res = append(res, *r)
		}
	}
	dbs, vhost := dropDatabases(ctx, d, m), deleteVhost(ctx, d, m)
	add(dbs)
	add(vhost)
	site, siteRemoved := removeSite(ctx, d, m, reload)
	add(site)
	port := releasePort(d, m)
	add(port)
	if kept = append(slices.Clone(failed), failedSteps(res)...); len(kept) > 0 {
		if err := keepManifest(d, m, dbs, vhost, port); err != nil {
			add(&StepResult{Step: "manifest", Err: err})
		}
		return res, siteRemoved, kept
	}
	r := StepResult{Step: "manifest"}
	if err := d.Reg.RemoveManifest(m.Label); err != nil {
		r.Err = err
	}
	add(&r)
	return res, siteRemoved, nil
}

// keepManifest rewrites a manifest that release keeps, without the claims
// that are settled: the bases once the databases step succeeded, the vhost
// once it was deleted, and the port once its link was removed or is no
// longer this worktree's (ports are reused lowest-first, so a stale port
// would make the next gc or up trip over someone else's claim). The failed
// claims stay for the next down or gc. The file keeps its owner.
func keepManifest(d Deps, m *registry.Manifest, dbs, vhost, port *StepResult) error {
	settled := func(r *StepResult) bool { return r != nil && r.Err == nil && !r.Skipped }
	c := *m
	changed := false
	if settled(dbs) {
		c.DBBases, changed = nil, true
	}
	if settled(vhost) {
		c.Vhost, changed = "", true
	}
	if port != nil && port.Err == nil { // released, or nothing of ours to release
		c.Port, changed = 0, true
	}
	if !changed {
		return nil
	}
	if err := d.Reg.WriteManifest(&c); err != nil {
		return fmt.Errorf("rewrite kept manifest %s: %w", m.Label, err)
	}
	return nil
}

// dropDatabases drops the databases matching the bases and suffix the
// manifest recorded, not the current project config. No bases is nothing to
// do, not a skip.
func dropDatabases(ctx context.Context, d Deps, m *registry.Manifest) *StepResult {
	if len(m.DBBases) == 0 {
		return nil
	}
	r := &StepResult{Step: "databases"}
	if d.PG == nil {
		r.Skipped, r.Reason = true, "this host has no postgres"
		return r
	}
	names, err := d.PG.List(ctx)
	if err != nil {
		r.Err = fmt.Errorf("list databases: %w", err)
		return r
	}
	var errs []error
	for _, name := range pg.Match(names, m.DBBases, m.DBSuffix) {
		if err := d.PG.Drop(ctx, name); err != nil {
			errs = append(errs, err)
		}
	}
	r.Err = errors.Join(errs...)
	return r
}

func deleteVhost(ctx context.Context, d Deps, m *registry.Manifest) *StepResult {
	if m.Vhost == "" {
		return nil
	}
	r := &StepResult{Step: "vhost"}
	if d.Rabbit == nil {
		r.Skipped, r.Reason = true, fmt.Sprintf("this host has no rabbitmq; vhost %s not deleted", m.Vhost)
		return r
	}
	if err := d.Rabbit.Delete(ctx, m.Vhost); err != nil {
		r.Err = fmt.Errorf("delete vhost %s: %w", m.Vhost, err)
	}
	return r
}

// removeSite removes the site file in server mode, when there is one.
func removeSite(ctx context.Context, d Deps, m *registry.Manifest, reload bool) (*StepResult, bool) {
	if d.Mode != config.Server {
		return nil, false
	}
	r := &StepResult{Step: "site"}
	path, err := caddy.SitePath(d.Reg.SitesDir, m.Label)
	if err != nil {
		r.Err = err
		return r, false
	}
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, false
	} else if err != nil {
		r.Err = err
		return r, false
	}
	if err := caddy.RemoveSite(d.Reg.SitesDir, m.Label); err != nil {
		r.Err = err
		return r, false
	}
	if reload {
		r.Err = caddy.SudoReload(ctx, d.Run, d.Bin)
	}
	return r, true
}

// releasePort releases the recorded port. A link that is missing or points
// at another worktree is nothing of ours to release: a skip with the reason,
// never a failure (the port may have been reused since a kept manifest
// released it).
func releasePort(d Deps, m *registry.Manifest) *StepResult {
	if m.Port == 0 {
		return nil
	}
	r := &StepResult{Step: "port"}
	target, err := d.Reg.PortOwner(m.Port)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.Skipped, r.Reason = true, fmt.Sprintf("port link %d is already gone", m.Port)
		return r
	case err != nil:
		r.Err = fmt.Errorf("release port %d: %w", m.Port, err)
		return r
	case target != m.Worktree:
		r.Skipped, r.Reason = true, fmt.Sprintf("port %d is now claimed by %s; nothing to release", m.Port, target)
		return r
	}
	err = d.Reg.ReleasePort(m.Port, m.Worktree)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.Skipped, r.Reason = true, fmt.Sprintf("port link %d is already gone", m.Port)
	case errors.Is(err, registry.ErrNotOwner):
		r.Skipped, r.Reason = true, fmt.Sprintf("port %d was claimed by another worktree meanwhile; nothing to release", m.Port)
	case err != nil:
		r.Err = fmt.Errorf("release port %d: %w", m.Port, err)
	}
	return r
}

// logResults writes one line per skipped or failed step.
func logResults(w io.Writer, prefix string, res []StepResult) {
	for _, r := range res {
		switch {
		case r.Skipped:
			fmt.Fprintf(w, "%s: skipped %s: %s\n", prefix, r.Step, r.Reason)
		case r.Err != nil:
			fmt.Fprintf(w, "%s: failed %s: %v\n", prefix, r.Step, r.Err)
		}
	}
}

// failure names the failed steps when strict is set; nil otherwise.
func failure(res []StepResult, strict bool) error {
	if !strict {
		return nil
	}
	var failed []string
	for _, r := range res {
		if r.Err != nil {
			failed = append(failed, r.Step)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("failed steps: %s", strings.Join(failed, ", "))
}
