package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/quotedo/hades/internal/envfile"
	"github.com/quotedo/hades/internal/execx"
)

const joinUsage = "usage: hades join <host-or-ip> [--as LOGIN] [--alias NAME]"

// join prepares this laptop to reach a hades box: an SSH key, the key on
// GitHub, a block in ~/.ssh/config, then a test login. It exits 0 when the
// login works, 2 while the box does not accept the key yet and 1 on errors.
func join(ctx *Context, args []string) int {
	fs := flag.NewFlagSet("join", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	login := fs.String("as", ctx.Getenv("USER"), "Linux login on the box")
	aliasFlag := fs.String("alias", "", "~/.ssh/config host alias")
	// Flags may come before or after the host.
	err := fs.Parse(args)
	arg := fs.Arg(0)
	if err == nil && fs.NArg() > 0 {
		err = fs.Parse(fs.Args()[1:])
	}
	if err == nil && (arg == "" || fs.NArg() > 0) {
		err = errors.New("expected one host or IP")
	}
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "join: %v\n%s\n", err, joinUsage)
		return 2
	}
	failf := func(format string, a ...any) int {
		fmt.Fprintf(ctx.Stderr, "join: "+format+"\n", a...)
		return 1
	}
	home := ctx.Getenv("HOME")
	switch {
	case home == "":
		return failf("HOME is not set")
	case *login == "":
		return failf("USER is not set; pass --as LOGIN")
	}
	host, alias, err := joinTarget(arg, *aliasFlag)
	if err != nil {
		return failf("%v", err)
	}
	bg, run := context.Background(), ctx.Runner

	pubPath, ok := findPublicKey(home)
	if !ok {
		hostname, _ := os.Hostname()
		key := filepath.Join(home, ".ssh", "id_ed25519")
		if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
			return failf("%v", err)
		}
		if _, err := run.Run(bg, execx.Cmd{Argv: []string{"ssh-keygen", "-t", "ed25519", "-N", "", "-f", key, "-C", ctx.Getenv("USER") + "@" + hostname}}); err != nil {
			return failf("%v", err)
		}
		fmt.Fprintf(ctx.Stderr, "generated %s\n", key)
		pubPath = key + ".pub"
	}
	pub, err := os.ReadFile(pubPath)
	if err != nil {
		return failf("%v", err)
	}

	ghLogin, err := githubKey(bg, run, pubPath, string(pub), alias)
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "github: %v; run gh auth login and then hades join again, or send your key to the administrator\n", err)
	}

	changed, err := envfile.UpsertBlock(filepath.Join(home, ".ssh", "config"), "# hades:begin "+alias+"\n", "# hades:end "+alias+"\n", sshConfigBlock(alias, host, *login))
	if err != nil {
		return failf("%v", err)
	}
	if changed {
		fmt.Fprintf(ctx.Stderr, "wrote Host %s to ~/.ssh/config\n", alias)
	}

	_, err = run.Run(bg, execx.Cmd{Argv: []string{"ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", alias, "true"}})
	var xe *execx.ExitError
	switch {
	case err == nil:
		fmt.Fprintf(ctx.Stdout, "connected: %s@%s\n", *login, alias)
		fmt.Fprintf(ctx.Stdout, "in Paseo Desktop choose Remote SSH, host %s, user %s; log in once over SSH to read your Paseo password and run hades-onboard\n", alias, *login)
		return 0
	case errors.As(err, &xe):
		fmt.Fprint(ctx.Stderr, xe.Result.Stderr)
		who := "your key below"
		if ghLogin != "" {
			who = "github: " + ghLogin + " (or your key below)"
		}
		fmt.Fprintf(ctx.Stdout, "not authorised yet: ask the administrator to add %s to your entry and apply, then run: hades join %s\n", who, strings.Join(args, " "))
		fmt.Fprint(ctx.Stdout, string(pub))
		return 2
	default:
		return failf("%v", err)
	}
}

// joinTarget returns the host to connect to and its ~/.ssh/config alias: the
// given alias, else the host's first DNS label. An IP needs an explicit alias.
func joinTarget(arg, alias string) (host, aliasOut string, err error) {
	if alias != "" {
		return arg, alias, nil
	}
	if net.ParseIP(arg) != nil {
		return "", "", fmt.Errorf("%s: an IP needs --alias", arg)
	}
	label, _, _ := strings.Cut(arg, ".")
	return arg, label, nil
}

// findPublicKey returns ~/.ssh/id_ed25519.pub, else the first ~/.ssh/*.pub.
func findPublicKey(home string) (pubPath string, found bool) {
	p := filepath.Join(home, ".ssh", "id_ed25519.pub")
	if _, err := os.Stat(p); err == nil {
		return p, true
	}
	m, _ := filepath.Glob(filepath.Join(home, ".ssh", "*.pub")) // sorted
	if len(m) == 0 {
		return "", false
	}
	return m[0], true
}

// githubKey makes sure the key is on the gh user's GitHub account and returns
// that account's login.
func githubKey(bg context.Context, run execx.Runner, pubPath, pub, alias string) (string, error) {
	gh := func(args ...string) (string, error) {
		res, err := run.Run(bg, execx.Cmd{Argv: append([]string{"gh"}, args...)})
		return strings.TrimSpace(res.Stdout), err
	}
	if _, err := gh("auth", "status"); err != nil {
		return "", errors.New("gh is not installed or not logged in")
	}
	login, err := gh("api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	list, err := gh("ssh-key", "list")
	if err != nil {
		return "", err
	}
	if f := strings.Fields(pub); len(f) >= 2 && strings.Contains(list, f[1]) {
		return login, nil
	}
	if _, err := gh("ssh-key", "add", pubPath, "--title", "hades "+alias); err != nil {
		return "", err
	}
	return login, nil
}

// sshConfigBlock is the ~/.ssh/config entry for the box, written between
// "# hades:begin <alias>" and "# hades:end <alias>".
func sshConfigBlock(alias, host, login string) string {
	return "Host " + alias + "\n  HostName " + host + "\n  User " + login + "\n  ForwardAgent yes\n"
}
