package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// join prepares this laptop to reach the developer's VM: an SSH key on GitHub,
// the VM's address from the box's jump account, <alias>-via-host through the
// box, a test login, hades-onboard in the VM, then <alias> over the tailnet
// when this laptop reaches the VM there. It exits with hades-onboard's code
// once the login works, 2 while the box does not accept the key yet and 1 on
// errors.
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
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(ctx.Stdout, joinUsage)
		return 0
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
	// ssh matches names without case; a Host block named like the box would
	// capture the jump to the box.
	if strings.EqualFold(host, alias) {
		fmt.Fprintf(ctx.Stderr, "join: %s: the alias must differ from the host; pass --alias NAME\n%s\n", host, joinUsage)
		return 2
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
		if ghUser == "" {
			fmt.Fprintln(ctx.Stderr, "github: gh is not logged in; your key was not added. Run gh auth login, then hades join again, or add the key below at https://github.com/settings/keys.")
		} else if ghUser != ghLogin {
			fmt.Fprintf(ctx.Stderr, "github: gh is logged in as %s, not %s; your key was not added. Add the key below at https://github.com/settings/keys.\n", ghUser, ghLogin)
		} else if res, err := run.Run(bg, execx.Cmd{Argv: []string{"gh", "ssh-key", "add", key.file, "--title", "hades " + alias}}); err == nil {
			onGitHub = true
		} else if strings.Contains(res.Stdout+res.Stderr, "admin:public_key") {
			fmt.Fprintln(ctx.Stderr, "github: gh lacks the admin:public_key scope; your key was not added. Run gh auth refresh -h github.com -s admin:public_key, then hades join again.")
		} else {
			fmt.Fprintf(ctx.Stderr, "github: %v; your key was not added.\n", err)
		}
	}

	identity := strings.TrimSuffix(key.file, ".pub")
	var idArg []string
	if identity != "" {
		idArg = []string{"-i", identity}
	}
	batch := []string{"ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=10"}
	// sshFailed reports a failed ssh through the box; a refused key exits 2.
	sshFailed := func(err error) int {
		var xe *execx.ExitError
		switch {
		case !errors.As(err, &xe):
			return failf("%v", err)
		case !strings.Contains(xe.Result.Stderr, "Permission denied"):
			return failf("ssh: %s", strings.TrimSpace(xe.Result.Stderr))
		}
		fmt.Fprint(ctx.Stderr, xe.Result.Stderr)
		fmt.Fprintln(ctx.Stdout, "not authorised yet: the box does not accept your key.")
		if onGitHub {
			fmt.Fprintf(ctx.Stdout, "Your key is on GitHub as %s: if the administrator has added you, the box picks it up within the hour (they can run: sudo hades-keys %s); otherwise ask them to add you with github: %s.\n", ghLogin, *login, ghLogin)
		} else {
			fmt.Fprintf(ctx.Stdout, "Your key is not on GitHub: send the key below to the administrator, or add it at https://github.com/settings/keys.\n%s\n", key.line)
		}
		fmt.Fprintf(ctx.Stdout, "Then run again: hades join %s\n", strings.Join(args, " "))
		return 2
	}
	upsert := func(name, hostName, proxy string) error {
		changed, err := envfile.UpsertBlock(filepath.Join(sshDir, "config"), "# hades:begin "+name+"\n", "# hades:end "+name+"\n", sshConfigBlock(name, hostName, *login, alias, proxy, identity))
		if changed {
			fmt.Fprintf(ctx.Stderr, "wrote Host %s to ~/.ssh/config\n", name)
		}
		return err
	}

	// The jump account's forced command prints the VM's address.
	res, err := run.Run(bg, execx.Cmd{Argv: slices.Concat(batch, idArg, []string{"-l", "hades", host})})
	if err != nil {
		return sshFailed(err)
	}
	addr := strings.TrimSpace(res.Stdout)
	if net.ParseIP(addr) == nil {
		return failf("ssh -l hades %s printed %q, not an address", host, addr)
	}
	via := alias + "-via-host"
	jump := strings.Join(slices.Concat([]string{"ssh", "-W", "%h:%p", "-l", "hades", "-o", "StrictHostKeyChecking=accept-new"}, idArg, []string{host}), " ")
	if err := upsert(via, addr, jump); err != nil {
		return failf("%v", err)
	}
	if _, err := run.Run(bg, execx.Cmd{Argv: slices.Concat(batch, []string{via, "true"})}); err != nil {
		return sshFailed(err)
	}
	fmt.Fprintf(ctx.Stdout, "connected: %s@%s\n", *login, alias)
	code := handOff([]string{"ssh", "-t", via, "hades-onboard", alias})
	if code == 127 {
		fmt.Fprintf(ctx.Stdout, "next: ssh %s\n", via)
		return 0
	}
	if code != 0 {
		return code
	}

	// Daily use goes over the developer's tailnet when this laptop reaches the
	// VM there; otherwise <alias> goes through the box like <alias>-via-host.
	var st struct {
		BackendState string
		Self         struct{ DNSName string }
	}
	res, _ = run.Run(bg, execx.Cmd{Argv: slices.Concat(batch, []string{via, "tailscale", "status", "--json"})})
	json.Unmarshal([]byte(res.Stdout), &st) // no tailnet when this fails
	tsName, hostName, proxy := strings.TrimSuffix(st.Self.DNSName, "."), addr, jump
	if st.BackendState == "Running" {
		// The VM's host key is known under the alias since the test login.
		if _, err := run.Run(bg, execx.Cmd{Argv: slices.Concat(batch, []string{"-o", "HostKeyAlias=" + alias, "-l", *login}, idArg, []string{tsName, "true"})}); err == nil {
			hostName, proxy = tsName, ""
		}
	}
	if err := upsert(alias, hostName, proxy); err != nil {
		return failf("%v", err)
	}
	switch {
	case proxy == "":
		fmt.Fprintf(ctx.Stdout, "daily: ssh %s (tailnet); fallback: ssh %s\n", alias, via)
	case st.BackendState == "Running":
		fmt.Fprintf(ctx.Stdout, "%s goes through the box: this laptop does not reach %s on your tailnet\n", alias, tsName)
	}
	return 0
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

// sshConfigBlock is the ~/.ssh/config entry for name, written between
// "# hades:begin <name>" and "# hades:end <name>". keyAlias names the host
// key in known_hosts whatever the HostName; proxy is the ProxyCommand, "" for
// a direct connection; identity is the private key file, "" for a key only in
// the agent.
func sshConfigBlock(name, hostName, login, keyAlias, proxy, identity string) string {
	b := "Host " + name + "\n  HostName " + hostName + "\n  User " + login + "\n  HostKeyAlias " + keyAlias + "\n"
	if proxy != "" {
		b += "  ProxyCommand " + proxy + "\n"
	}
	b += "  ForwardAgent yes\n  StrictHostKeyChecking accept-new\n"
	if identity != "" {
		b += "  IdentityFile " + identity + "\n"
	}
	return b
}
