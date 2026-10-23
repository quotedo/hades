package cli

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/quotedo/hades/internal/execx"
	"github.com/quotedo/hades/internal/naming"
	"github.com/quotedo/hades/internal/preset"
)

const initUsage = "usage: hades init [--ade paseo,orca,wt] [--force]"

var (
	//go:embed templates/hades.toml.tmpl
	hadesTomlTmpl string
	//go:embed templates/paseo.json
	paseoJSON string
	//go:embed templates/orca.yaml
	orcaYAML string
	//go:embed templates/wt-snippet.txt
	wtSnippet string
	//go:embed templates/phoenix-config-snippet.txt
	phoenixSnippet string
)

// Lines to add by hand to an existing hook file that lacks hades.
const (
	paseoAdd = "\"worktree\": {\n  \"setup\": [\"hades up\"],\n  \"teardown\": [\"hades down\"],\n  \"servicePorts\": {\"portScript\": \"hades-port\"}\n}\n"
	orcaAdd  = "scripts:\n  setup: hades up\n  archive: hades down\n"
)

var gitignoreEntries = []string{".env.worktree", ".envrc"}

// initCmd onboards the current repository: hades.toml, ADE hook files and
// .gitignore entries, plus the snippets that must be added by hand.
func initCmd(ctx *Context, args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	// --ade is also a global flag, so Main usually consumes it before init
	// sees the arguments; the flag here covers the `--` escape.
	defaultADE := "paseo,orca"
	if ctx.Flags.ADE != "" {
		defaultADE = ctx.Flags.ADE
	}
	ades := fs.String("ade", defaultADE, "ADEs to wire: paseo, orca, wt")
	force := fs.Bool("force", false, "overwrite existing hades.toml, paseo.json and orca.yaml")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(ctx.Stderr, "init: %v\n%s\n", err, initUsage)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(ctx.Stderr, "init: unexpected arguments %q\n%s\n", fs.Args(), initUsage)
		return 2
	}
	want := map[string]bool{}
	for _, a := range strings.Split(*ades, ",") {
		a = strings.TrimSpace(a)
		if a != "paseo" && a != "orca" && a != "wt" {
			fmt.Fprintf(ctx.Stderr, "init: unknown ADE %q\n%s\n", a, initUsage)
			return 2
		}
		want[a] = true
	}

	root, err := repoRoot()
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: init: %v\n", err)
		return 1
	}
	p, err := preset.Detect(root)
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: init: %v\n", err)
		return 1
	}

	w := &initWriter{root: root, stderr: ctx.Stderr, force: *force}
	w.file("hades.toml", renderHadesToml(root))
	if want["paseo"] {
		w.hook("paseo.json", paseoJSON, paseoAdd)
	}
	if want["orca"] {
		w.hook("orca.yaml", orcaYAML, orcaAdd)
	}
	w.gitignore()
	if w.err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: init: %v\n", w.err)
		return 1
	}

	if want["wt"] {
		fmt.Fprintf(ctx.Stderr, "hades: init: add to .config/wt.toml, inside the hooks table:\n%s", wtSnippet)
	}
	if p.Kind == preset.Phoenix || p.Kind == preset.Elixir {
		fmt.Fprintf(ctx.Stderr, "hades: init: set these in config/dev.exs and config/test.exs so each worktree gets its own port and databases:\n%s", renderPhoenixSnippet(p.AppName))
	}
	return 0
}

// repoRoot returns the top level of the repository containing the current
// directory.
func repoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	res, err := (&execx.Real{}).Run(context.Background(), execx.Cmd{Argv: []string{"git", "-C", cwd, "rev-parse", "--show-toplevel"}})
	var exit *execx.ExitError
	if err != nil && !errors.As(err, &exit) {
		return "", err
	}
	top := strings.TrimSpace(res.Stdout)
	if err != nil || top == "" {
		return "", errors.New("not inside a git repository")
	}
	return top, nil
}

func renderHadesToml(root string) string {
	line := `# project = "my-project"`
	if slug, err := naming.Slug(filepath.Base(root)); err == nil {
		line = fmt.Sprintf("project = %q", slug)
	}
	return strings.Replace(hadesTomlTmpl, "@PROJECT@", line, 1)
}

func renderPhoenixSnippet(app string) string {
	if app == "" {
		app = "my_app"
	}
	var mod strings.Builder
	for _, part := range strings.Split(app, "_") {
		if part != "" {
			mod.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return strings.NewReplacer("@APP@", app, "@MOD@", mod.String()).Replace(phoenixSnippet)
}

// initWriter writes files under root and reports each one on stderr. The
// first filesystem error sticks and later operations become no-ops.
type initWriter struct {
	root   string
	stderr io.Writer
	force  bool
	err    error
}

func (w *initWriter) read(name string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(w.root, name))
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		w.err = err
		return "", false
	}
	return string(b), true
}

func (w *initWriter) write(name, content string) {
	if err := os.WriteFile(filepath.Join(w.root, name), []byte(content), 0o644); err != nil {
		w.err = err
		return
	}
	fmt.Fprintf(w.stderr, "hades: init: wrote %s\n", name)
}

// file writes name unless it exists and --force is off.
func (w *initWriter) file(name, content string) {
	if w.err != nil {
		return
	}
	if _, exists := w.read(name); exists && !w.force {
		fmt.Fprintf(w.stderr, "hades: init: %s exists; leaving it (use --force to overwrite)\n", name)
		return
	}
	if w.err == nil {
		w.write(name, content)
	}
}

// hook writes an ADE hook file, or explains what to add to an existing one.
func (w *initWriter) hook(name, content, add string) {
	if w.err != nil {
		return
	}
	existing, exists := w.read(name)
	switch {
	case w.err != nil:
	case !exists || w.force:
		w.write(name, content)
	case strings.Contains(existing, "hades up"):
		fmt.Fprintf(w.stderr, "hades: init: %s already wired\n", name)
	default:
		fmt.Fprintf(w.stderr, "hades: init: %s exists without hades hooks; add:\n%s", name, add)
	}
}

// gitignore appends the entries that are missing from .gitignore.
func (w *initWriter) gitignore() {
	if w.err != nil {
		return
	}
	cur, _ := w.read(".gitignore")
	if w.err != nil {
		return
	}
	have := map[string]bool{}
	for _, l := range strings.Split(cur, "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var add strings.Builder
	for _, e := range gitignoreEntries {
		if !have[e] {
			add.WriteString(e + "\n")
		}
	}
	if add.Len() == 0 {
		return
	}
	if cur != "" && !strings.HasSuffix(cur, "\n") {
		cur += "\n"
	}
	w.write(".gitignore", cur+add.String())
}
