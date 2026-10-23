package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/quotedo/hades/internal/caddy"
	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/envfile"
	"github.com/quotedo/hades/internal/execx"
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
// drops the databases, deletes the vhost,
// removes the site, releases the port and removes the manifest. The steps run
// whether or not the earlier ones succeeded, and the manifest goes even when
// some failed, so the next up starts clean. Every skip and failure is a
// StepResult and a line on d.Log. Down returns an error only when strict is
// set and a step failed.
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
	rel, _ := release(ctx, d, m, true)
	logResults(d.Log, "down", rel)
	res = append(res, rel...)
	return res, failure(res, strict)
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
		if reason := verifyClaims(d, m, "down"); reason != "" {
			r.Err = fmt.Errorf("refusing %s: %s", m.Path(), reason)
			return nil, false, r
		}
		return m, false, r
	default:
		return m, true, r
	}
	return nil, false, r
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

// release undoes what m records: databases, vhost, site, port, manifest.
// With reload set, removing a site also reloads Caddy; GC passes false and
// reloads once at the end. siteRemoved reports whether a site file went.
func release(ctx context.Context, d Deps, m *registry.Manifest, reload bool) (res []StepResult, siteRemoved bool) {
	add := func(r *StepResult) {
		if r != nil {
			res = append(res, *r)
		}
	}
	add(dropDatabases(ctx, d, m))
	add(deleteVhost(ctx, d, m))
	site, siteRemoved := removeSite(ctx, d, m, reload)
	add(site)
	add(releasePort(d, m))
	r := StepResult{Step: "manifest"}
	if err := d.Reg.RemoveManifest(m.Label); err != nil {
		r.Err = err
	}
	add(&r)
	return res, siteRemoved
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

// releasePort releases the recorded port. A missing link or one claimed by
// another worktree is a failure: the manifest says the port is ours.
func releasePort(d Deps, m *registry.Manifest) *StepResult {
	if m.Port == 0 {
		return nil
	}
	r := &StepResult{Step: "port"}
	if err := d.Reg.ReleasePort(m.Port, m.Worktree); err != nil {
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
