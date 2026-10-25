package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/quotedo/hades/internal/envfile"
	"github.com/quotedo/hades/internal/execx"
)

const joinUsage = "usage: hades join <host-or-ip> [--as LOGIN] [--alias NAME] [--github LOGIN]"

// keysURL is where GitHub serves a user's public keys; tests point it at a
// local server.
var keysURL = "https://github.com/%s.keys"

// handOff runs argv attached to this process's terminal and returns its exit
// code; tests replace it.
var handOff = func(argv []string) int {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var xe *exec.ExitError
	switch {
	case errors.As(err, &xe):
		return xe.ExitCode()
	case err != nil:
		fmt.Fprintf(os.Stderr, "join: %v\n", err)
		return 1
	}
	return 0
}

// sshKey is a public key line, read from file ("" for a key only in the agent).
type sshKey struct{ line, file string }

func (k sshKey) blob() string {
	if f := strings.Fields(k.line); len(f) >= 2 {
		return f[1]
	}
	return ""
}

// join prepares this laptop to reach a hades box: an SSH key on GitHub, a
// block in ~/.ssh/config, a test login, then hades-onboard on the box. It
// exits with hades-onboard's code once the login works, 2 while the box does
// not accept the key yet and 1 on errors.
func join(ctx *Context, args []string) int {
	fs := flag.NewFlagSet("join", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	login := fs.String("as", ctx.Getenv("USER"), "Linux login on the box")
	aliasFlag := fs.String("alias", "", "~/.ssh/config host alias")
	githubFlag := fs.String("github", "", "GitHub login")
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
	sshDir := filepath.Join(home, ".ssh")

	// Files first, so a key that is in the agent too gets its IdentityFile.
	files := localKeys(sshDir)
	keys := files
	if res, err := run.Run(bg, execx.Cmd{Argv: []string{"ssh-add", "-L"}}); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
			keys = append(keys, sshKey{line: l})
		}
	}

	ghUser := ""
	if res, err := run.Run(bg, execx.Cmd{Argv: []string{"gh", "api", "user", "--jq", ".login"}}); err == nil {
		ghUser = strings.TrimSpace(res.Stdout)
	}
	ghLogin := *githubFlag
	if ghLogin == "" {
		ghLogin = ghUser
	}
	served := map[string]bool{}
	if ghLogin != "" {
		body, err := fetchKeys(ghLogin)
		if err != nil {
			fmt.Fprintf(ctx.Stderr, "github: %v\n", err)
		}
		for _, l := range strings.Split(body, "\n") {
			served[sshKey{line: l}.blob()] = true
		}
	}

	var key sshKey
	onGitHub := false
	for _, k := range keys {
		if k.blob() != "" && served[k.blob()] {
			key, onGitHub = k, true
			break
		}
	}
	if !onGitHub {
		if len(files) == 0 {
			hostname, _ := os.Hostname()
			priv := filepath.Join(sshDir, "id_ed25519")
			if err := os.MkdirAll(sshDir, 0o700); err != nil {
				return failf("%v", err)
			}
			if _, err := run.Run(bg, execx.Cmd{Argv: []string{"ssh-keygen", "-t", "ed25519", "-N", "", "-f", priv, "-C", ctx.Getenv("USER") + "@" + hostname}}); err != nil {
				return failf("%v", err)
			}
			fmt.Fprintf(ctx.Stderr, "generated %s\n", priv)
			if files = localKeys(sshDir); len(files) == 0 {
				return failf("ssh-keygen wrote no %s.pub", priv)
			}
		}
		key = files[0]
		for _, k := range files {
			if filepath.Base(k.file) == "id_ed25519.pub" {
				key = k
			}
		}
		if ghUser == "" || ghUser != ghLogin {
			fmt.Fprintln(ctx.Stderr, "github: gh is not logged in; your key was not added. Run gh auth login, then hades join again, or add the key below at https://github.com/settings/keys.")
		} else if res, err := run.Run(bg, execx.Cmd{Argv: []string{"gh", "ssh-key", "add", key.file, "--title", "hades " + alias}}); err == nil {
			onGitHub = true
		} else if strings.Contains(res.Stdout+res.Stderr, "admin:public_key") {
			fmt.Fprintln(ctx.Stderr, "github: gh lacks the admin:public_key scope; your key was not added. Run gh auth refresh -h github.com -s admin:public_key, then hades join again.")
		} else {
			fmt.Fprintf(ctx.Stderr, "github: %v; your key was not added.\n", err)
		}
	}

	identity := strings.TrimSuffix(key.file, ".pub")
	changed, err := envfile.UpsertBlock(filepath.Join(sshDir, "config"), "# hades:begin "+alias+"\n", "# hades:end "+alias+"\n", sshConfigBlock(alias, host, *login, identity))
	if err != nil {
		return failf("%v", err)
	}
	if changed {
		fmt.Fprintf(ctx.Stderr, "wrote Host %s to ~/.ssh/config\n", alias)
	}

	_, err = run.Run(bg, execx.Cmd{Argv: []string{"ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=10", alias, "true"}})
	var xe *execx.ExitError
	errors.As(err, &xe)
	switch {
	case err == nil:
		fmt.Fprintf(ctx.Stdout, "connected: %s@%s\n", *login, alias)
		code := handOff([]string{"ssh", "-t", alias, "hades-onboard", alias})
		if code == 127 {
			fmt.Fprintf(ctx.Stdout, "next: ssh %s\n", alias)
			return 0
		}
		return code
	case xe != nil && strings.Contains(xe.Result.Stderr, "Permission denied"):
		fmt.Fprint(ctx.Stderr, xe.Result.Stderr)
		fmt.Fprintln(ctx.Stdout, "not authorised yet: the box does not accept your key.")
		if onGitHub {
			fmt.Fprintf(ctx.Stdout, "Your key is on GitHub as %s: if the administrator has added you, the box picks it up within the hour (they can run: sudo hades-keys %s); otherwise ask them to add you with github: %s.\n", ghLogin, *login, ghLogin)
		} else {
			fmt.Fprintf(ctx.Stdout, "Your key is not on GitHub: send the key below to the administrator, or add it at https://github.com/settings/keys.\n%s\n", key.line)
		}
		fmt.Fprintf(ctx.Stdout, "Then run again: hades join %s\n", strings.Join(args, " "))
		return 2
	case xe != nil:
		return failf("ssh: %s", strings.TrimSpace(xe.Result.Stderr))
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

// localKeys returns the keys in dir/*.pub, sorted by file name.
func localKeys(dir string) []sshKey {
	files, _ := filepath.Glob(filepath.Join(dir, "*.pub")) // sorted
	var keys []sshKey
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			keys = append(keys, sshKey{line: strings.TrimSpace(string(b)), file: f})
		}
	}
	return keys
}

// fetchKeys returns the public keys GitHub serves for login, one per line.
func fetchKeys(login string) (string, error) {
	url := fmt.Sprintf(keysURL, login)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

// sshConfigBlock is the ~/.ssh/config entry for the box, written between
// "# hades:begin <alias>" and "# hades:end <alias>". identity is the private
// key file, "" for a key only in the agent.
func sshConfigBlock(alias, host, login, identity string) string {
	b := "Host " + alias + "\n  HostName " + host + "\n  User " + login + "\n  ForwardAgent yes\n  StrictHostKeyChecking accept-new\n"
	if identity != "" {
		b += "  IdentityFile " + identity + "\n"
	}
	return b
}
