package cli

import (
	"bytes"
	"cmp"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/quotedo/hades/internal/envfile"
	"github.com/quotedo/hades/internal/execx"
)

func TestJoinTarget(t *testing.T) {
	for _, tc := range []struct{ arg, alias, host, wantAlias string }{
		{"shop.example.test", "", "shop.example.test", "shop"},
		{"shop", "", "shop", "shop"},
		{"shop.example.test", "box", "shop.example.test", "box"},
		{"203.0.113.10", "shop", "203.0.113.10", "shop"},
		{"2001:db8::1", "shop", "2001:db8::1", "shop"},
	} {
		host, alias, err := joinTarget(tc.arg, tc.alias)
		if err != nil || host != tc.host || alias != tc.wantAlias {
			t.Errorf("joinTarget(%q, %q) = %q, %q, %v", tc.arg, tc.alias, host, alias, err)
		}
	}
	if _, _, err := joinTarget("203.0.113.10", ""); err == nil || !strings.Contains(err.Error(), "an IP needs --alias") {
		t.Fatalf("IP without alias: %v", err)
	}
}

func TestUpsertSSHConfigKeepsOtherContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ssh", "config")
	upsertSSHConfig := func(path, alias, block string) (bool, error) {
		return envfile.UpsertBlock(path, "# hades:begin "+alias+"\n", "# hades:end "+alias+"\n", block)
	}
	changed, err := upsertSSHConfig(path, "shop", sshConfigBlock("shop", "shop.example.test", "alice", "shop", "", ""))
	if err != nil || !changed {
		t.Fatalf("create: %v %v", changed, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Dir(path)); fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", fi.Mode().Perm())
	}

	before := "Host other\n  User bob\n\n"
	after := "\nHost later\n  User carol" // no trailing newline
	block := "# hades:begin shop\nHost shop\n  HostName shop.example.test\n  User alice\n  HostKeyAlias shop\n  ForwardAgent yes\n  StrictHostKeyChecking accept-new\n# hades:end shop\n"
	os.WriteFile(path, []byte(before+block+after), 0o644)
	os.Chmod(path, 0o644)

	changed, err = upsertSSHConfig(path, "shop", sshConfigBlock("shop", "shop.example.test", "alice", "shop", "", ""))
	if err != nil || changed {
		t.Fatalf("identical rerun: %v %v", changed, err)
	}
	changed, err = upsertSSHConfig(path, "shop", sshConfigBlock("shop", "203.0.113.10", "alice", "shop", "", ""))
	if err != nil || !changed {
		t.Fatalf("changed host: %v %v", changed, err)
	}
	want := before + strings.Replace(block, "shop.example.test", "203.0.113.10", 1) + after
	if got := string(mustRead(t, path)); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Fatalf("existing file mode changed to %v", fi.Mode().Perm())
	}

	// A second alias is appended; the first block is untouched.
	if _, err := upsertSSHConfig(path, "sh", sshConfigBlock("sh", "sh.example.test", "alice", "sh", "", "")); err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, path))
	if !strings.HasPrefix(got, want+"\n# hades:begin sh\nHost sh\n") || strings.Count(got, "# hades:begin") != 2 {
		t.Fatalf("second alias:\n%s", got)
	}
}

// joinCase is what the fake world answers during one join.
type joinCase struct {
	agent   string // ssh-add -L output
	gh      string // gh api user login; "" means gh is missing or logged out
	addErr  string // gh ssh-key add fails with this output
	served  string // body of https://github.com/alice-gh.keys
	sshErr  string // every ssh fails with this stderr
	jumpOut string // what ssh -l hades prints; "" means alice@10.88.7.209
	vmErr   string // the login through <alias>-via-host fails with this stderr
	handoff int    // exit code of the hand-off
	status  string // tailscale status --json in the VM
	tsErr   string // the ssh tests over the tailnet fail with this stderr,
	tsUp    string // except the one to this host
}

type joinRun struct {
	code        int
	out, errOut string
	f           *execx.Fake
	handoff     []string
}

func runJoin(t *testing.T, home string, jc joinCase, args ...string) joinRun {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/alice-gh.keys" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, jc.served)
	}))
	t.Cleanup(srv.Close)
	prevURL, prevHandOff := keysURL, handOff
	t.Cleanup(func() { keysURL, handOff = prevURL, prevHandOff })
	keysURL = srv.URL + "/%s.keys"
	var r joinRun
	handOff = func(argv []string) int { r.handoff = argv; return jc.handoff }

	r.f = &execx.Fake{Handle: func(c execx.Cmd) (execx.Result, error) {
		fail := func(stderr string) (execx.Result, error) {
			res := execx.Result{ExitCode: 1, Stderr: stderr}
			return res, &execx.ExitError{Cmd: c, Result: res}
		}
		switch a := strings.Join(c.Argv, " "); {
		case a == "ssh-add -L":
			return execx.Result{Stdout: jc.agent}, nil
		case strings.HasPrefix(a, "gh ") && jc.gh == "":
			return execx.Result{}, exec.ErrNotFound
		case a == "gh api user --jq .login":
			return execx.Result{Stdout: jc.gh + "\n"}, nil
		case strings.HasPrefix(a, "gh ssh-key add") && jc.addErr != "":
			return fail(jc.addErr)
		case c.Argv[0] == "ssh-keygen":
			os.WriteFile(c.Argv[6]+".pub", []byte("ssh-ed25519 AAAAnew alice@laptop\n"), 0o644)
		case c.Argv[0] == "ssh" && jc.sshErr != "":
			return fail(jc.sshErr)
		case strings.Contains(a, " -l hades "):
			return execx.Result{Stdout: cmp.Or(jc.jumpOut, "alice@10.88.7.209\n")}, nil
		case strings.HasSuffix(a, "-via-host true") && jc.vmErr != "":
			return fail(jc.vmErr)
		case strings.HasSuffix(a, " tailscale status --json"):
			return execx.Result{Stdout: jc.status}, nil
		case strings.Contains(a, " -l alice ") && jc.tsErr != "" && !strings.HasSuffix(a, " "+jc.tsUp+" true"):
			return fail(jc.tsErr)
		}
		return execx.Result{}, nil
	}}
	env := map[string]string{"HOME": home, "USER": "alice"}
	var out, errOut bytes.Buffer
	ctx := &Context{Stdout: &out, Stderr: &errOut, Getenv: func(k string) string { return env[k] }, Runner: r.f}
	r.code = join(ctx, args)
	r.out, r.errOut = out.String(), errOut.String()
	return r
}

// homeWithKeys writes ~/.ssh/<name>.pub for each name, the blob being
// AAAA<name>.
func homeWithKeys(t *testing.T, names ...string) string {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	for _, n := range names {
		os.WriteFile(filepath.Join(home, ".ssh", n+".pub"), []byte("ssh-ed25519 AAAA"+n+" alice@laptop\n"), 0o644)
	}
	return home
}

func called(f *execx.Fake, argv ...string) bool {
	return slices.ContainsFunc(f.Calls, func(c execx.Cmd) bool { return slices.Equal(c.Argv, argv) })
}

func calledPrefix(f *execx.Fake, argv ...string) bool {
	return slices.ContainsFunc(f.Calls, func(c execx.Cmd) bool { return len(c.Argv) >= len(argv) && slices.Equal(c.Argv[:len(argv)], argv) })
}

const (
	notLoggedIn = "github: gh is not logged in; your key was not added. Run gh auth login, then hades join again, or add the key below at https://github.com/settings/keys.\n"
	refused     = "Permission denied (publickey).\n"
	tsRunning   = `{"BackendState":"Running","Self":{"DNSName":"box.tail1234.ts.net.","TailscaleIPs":["100.64.0.7","fd7a:115c:a1e0::7"]}}`
)

func sshConfig(t *testing.T, home string) string {
	return string(mustRead(t, filepath.Join(home, ".ssh", "config")))
}

// withKey is what join passes to ssh for the key file key.
func withKey(key string) []string { return []string{"-i", key, "-o", "IdentitiesOnly=yes"} }

// jumpCall is the ssh to the jump account of host with the key file key.
func jumpCall(key, host string) []string {
	return sshBatch(slices.Concat(withKey(key), []string{"-l", "hades", host})...)
}

// tailnetProbe is the test login over the tailnet to host.
func tailnetProbe(key, host string) []string {
	return sshBatch(slices.Concat([]string{"-o", "HostKeyAlias=box", "-l", "alice"}, withKey(key), []string{host, "true"})...)
}

// sshBatch is the argv of an ssh call that must not prompt.
func sshBatch(args ...string) []string {
	return append([]string{"ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=10"}, args...)
}

// viaBox is the block for name through the jump account of box.example.test,
// with the key ~/.ssh/id_ed25519 and the host key stored as box.
func viaBox(home, name string) string {
	key := filepath.Join(home, ".ssh", "id_ed25519")
	return "# hades:begin " + name + "\nHost " + name + "\n  HostName 10.88.7.209\n  User alice\n  HostKeyAlias box\n" +
		"  ProxyCommand ssh -W %h:%p -l hades -o StrictHostKeyChecking=accept-new -i " + key + " -o IdentitiesOnly=yes box.example.test\n" +
		"  ForwardAgent yes\n  StrictHostKeyChecking accept-new\n  IdentityFile " + key + "\n  IdentitiesOnly yes\n# hades:end " + name + "\n"
}

func TestJoinLearnsVMAddressFromJumpAccount(t *testing.T) {
	// No tailnet in the VM: both aliases go through the box.
	home := homeWithKeys(t, "id_ed25519")
	key := filepath.Join(home, ".ssh", "id_ed25519")
	r := runJoin(t, home, joinCase{}, "box.example.test")
	// The box accepts the key, so the gh hint (notLoggedIn) is not printed.
	if r.code != 0 || r.out != "connected: alice@box\n" || r.errOut != "wrote Host box-via-host to ~/.ssh/config\nwrote Host box to ~/.ssh/config\n" {
		t.Fatalf("%d %q %q", r.code, r.out, r.errOut)
	}
	if !called(r.f, jumpCall(key, "box.example.test")...) || !called(r.f, sshBatch("box-via-host", "true")...) ||
		!slices.Equal(r.handoff, []string{"ssh", "-t", "-o", "LogLevel=ERROR", "box-via-host", "hades-onboard", "box"}) {
		t.Fatal(r.f.Calls, r.handoff)
	}
	if got := sshConfig(t, home); got != viaBox(home, "box-via-host")+viaBox(home, "box") {
		t.Fatal(got)
	}
}

func TestJoinSwitchesAliasToTailnetAfterOnboarding(t *testing.T) {
	home := homeWithKeys(t, "id_ed25519")
	key := filepath.Join(home, ".ssh", "id_ed25519")
	r := runJoin(t, home, joinCase{status: tsRunning}, "box.example.test")
	if r.code != 0 || r.out != "connected: alice@box\ndaily: ssh box (tailnet); fallback: ssh box-via-host\n" {
		t.Fatalf("%d %q", r.code, r.out)
	}
	if !called(r.f, sshBatch("box-via-host", "tailscale", "status", "--json")...) || !called(r.f, tailnetProbe(key, "box.tail1234.ts.net")...) {
		t.Fatal(r.f.Calls)
	}
	tailnet := "# hades:begin box\nHost box\n  HostName box.tail1234.ts.net\n  User alice\n  HostKeyAlias box\n  ForwardAgent yes\n  StrictHostKeyChecking accept-new\n  IdentityFile " + key + "\n  IdentitiesOnly yes\n# hades:end box\n"
	if got := sshConfig(t, home); got != viaBox(home, "box-via-host")+tailnet {
		t.Fatal(got)
	}
}

func TestJoinKeepsHostPathWhenTailnetUnreachable(t *testing.T) {
	home := homeWithKeys(t, "id_ed25519")
	r := runJoin(t, home, joinCase{status: tsRunning, tsErr: "ssh: connect to host box.tail1234.ts.net port 22: Operation timed out\n"}, "box.example.test")
	if r.code != 0 || r.out != "connected: alice@box\nbox goes through the box: this laptop does not reach box.tail1234.ts.net on your tailnet\n" {
		t.Fatalf("%d %q", r.code, r.out)
	}
	if got := sshConfig(t, home); got != viaBox(home, "box-via-host")+viaBox(home, "box") {
		t.Fatal(got)
	}
}

func TestJoinReachesTailnetByAddressWithoutItsDNS(t *testing.T) {
	// This laptop does not use Tailscale's DNS: the name fails, the address works.
	home := homeWithKeys(t, "id_ed25519")
	key := filepath.Join(home, ".ssh", "id_ed25519")
	r := runJoin(t, home, joinCase{status: tsRunning, tsErr: "ssh: Could not resolve hostname box.tail1234.ts.net\n", tsUp: "100.64.0.7"}, "box.example.test")
	want := "connected: alice@box\nthis laptop does not resolve box.tail1234.ts.net (Tailscale DNS off): using 100.64.0.7\ndaily: ssh box (tailnet); fallback: ssh box-via-host\n"
	if r.code != 0 || r.out != want {
		t.Fatalf("%d %q", r.code, r.out)
	}
	if !called(r.f, tailnetProbe(key, "100.64.0.7")...) || called(r.f, tailnetProbe(key, "fd7a:115c:a1e0::7")...) {
		t.Fatal(r.f.Calls)
	}
	tailnet := "# hades:begin box\nHost box\n  HostName 100.64.0.7\n  User alice\n  HostKeyAlias box\n  ForwardAgent yes\n  StrictHostKeyChecking accept-new\n  IdentityFile " + key + "\n  IdentitiesOnly yes\n# hades:end box\n"
	if got := sshConfig(t, home); got != viaBox(home, "box-via-host")+tailnet {
		t.Fatal(got)
	}
}

func TestJoinRerunAfterOnboarding(t *testing.T) {
	home := homeWithKeys(t, "id_ed25519")
	runJoin(t, home, joinCase{status: tsRunning}, "box.example.test")
	first := sshConfig(t, home)
	r := runJoin(t, home, joinCase{status: tsRunning}, "box.example.test")
	if r.code != 0 || r.out != "connected: alice@box\ndaily: ssh box (tailnet); fallback: ssh box-via-host\n" || strings.Contains(r.errOut, "wrote Host") || sshConfig(t, home) != first {
		t.Fatalf("%d %q %q\n%s", r.code, r.out, r.errOut, sshConfig(t, home))
	}
}

func TestJoinPermissionDeniedOnJumpAccount(t *testing.T) {
	home := homeWithKeys(t, "id_ed25519")
	r := runJoin(t, home, joinCase{sshErr: refused}, "box.example.test")
	if r.code != 2 || !strings.HasPrefix(r.out, "not authorised yet: the box does not accept your key.\n") || !strings.Contains(r.errOut, refused) || r.handoff != nil {
		t.Fatalf("%d %q %q", r.code, r.out, r.errOut)
	}
	if !called(r.f, jumpCall(filepath.Join(home, ".ssh", "id_ed25519"), "box.example.test")...) {
		t.Fatal(r.f.Calls)
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh", "config")); !os.IsNotExist(err) {
		t.Fatal("wrote ~/.ssh/config:", err)
	}
}

func TestJoinVMRefusesLogin(t *testing.T) {
	// The jump account answered, so the box accepts the key: no GitHub hints.
	for args, want := range map[string]string{
		"--as bob": "bob: pass --as <your inventory name>\n",
		// The login came from the box: the VM's copy of the key lags.
		"": "alice: the box copies your key to the VM within the hour (the administrator can run: sudo hades-keys alice)\n",
	} {
		home := homeWithKeys(t, "id_ed25519")
		r := runJoin(t, home, joinCase{vmErr: refused}, append([]string{"box.example.test"}, strings.Fields(args)...)...)
		want = "not authorised yet: the box accepted your key; the VM refused login " + want
		if r.code != 2 || r.out != want || !strings.Contains(r.errOut, refused) || strings.Contains(r.errOut, "github:") || r.handoff != nil {
			t.Fatalf("%q: %d %q %q", args, r.code, r.out, r.errOut)
		}
	}
}

func TestJoinLoginFromJumpAccount(t *testing.T) {
	for _, tc := range []struct {
		jumpOut string
		args    []string
		login   string // "" when join fails
	}{
		{"carol@10.88.7.209\n", nil, "carol"},
		{"carol@10.88.7.209\n", []string{"--as", "bob"}, "bob"},
		{"10.88.7.209\n", []string{"--as", "bob"}, "bob"}, // a box that prints only the address
		{"10.88.7.209\n", nil, ""},
	} {
		home := homeWithKeys(t, "id_ed25519")
		r := runJoin(t, home, joinCase{jumpOut: tc.jumpOut}, append([]string{"box.example.test"}, tc.args...)...)
		switch {
		case tc.login == "":
			if r.code != 1 || !strings.HasSuffix(r.errOut, "join: ssh -l hades box.example.test printed no login; pass --as LOGIN\n") || r.handoff != nil {
				t.Fatalf("%q: %d %q", tc.jumpOut, r.code, r.errOut)
			}
		case r.code != 0 || r.out != "connected: "+tc.login+"@box\n" || !strings.Contains(sshConfig(t, home), "  HostName 10.88.7.209\n  User "+tc.login+"\n"):
			t.Fatalf("%q %v: %d %q %q", tc.jumpOut, tc.args, r.code, r.out, r.errOut)
		}
	}
}

func TestJoinSkipsTailnetWithoutRunningName(t *testing.T) {
	// Logged out, or Running without a name: no tailnet login is tried.
	for _, status := range []string{
		`{"BackendState":"NeedsLogin","Self":{"DNSName":"","TailscaleIPs":[]}}`,
		`{"BackendState":"Running","Self":{"DNSName":"","TailscaleIPs":["100.64.0.7"]}}`,
	} {
		home := homeWithKeys(t, "id_ed25519")
		r := runJoin(t, home, joinCase{status: status}, "box.example.test")
		if r.code != 0 || r.out != "connected: alice@box\n" || calledPrefix(r.f, sshBatch("-o", "HostKeyAlias=box")...) || sshConfig(t, home) != viaBox(home, "box-via-host")+viaBox(home, "box") {
			t.Fatalf("%s: %d %q %v", status, r.code, r.out, r.f.Calls)
		}
	}
}

func TestJoinUnwritableSSHConfig(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes a read-only file")
	}
	home := homeWithKeys(t, "id_ed25519")
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), nil, 0o400); err != nil {
		t.Fatal(err)
	}
	r := runJoin(t, home, joinCase{}, "box.example.test")
	if r.code != 1 || strings.Contains(r.errOut, "wrote Host") || !strings.Contains(r.errOut, "permission denied") || r.handoff != nil {
		t.Fatalf("%d %q", r.code, r.errOut)
	}
}

func TestJoinRefusesJumpOutputThatIsNoAddress(t *testing.T) {
	// An empty HostName would break every ssh that reads ~/.ssh/config.
	home := homeWithKeys(t, "id_ed25519")
	r := runJoin(t, home, joinCase{jumpOut: "\n"}, "box.example.test")
	if r.code != 1 || !strings.HasSuffix(r.errOut, "join: ssh -l hades box.example.test printed \"\", not an address\n") || r.handoff != nil {
		t.Fatalf("%d %q", r.code, r.errOut)
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh", "config")); !os.IsNotExist(err) {
		t.Fatal("wrote ~/.ssh/config:", err)
	}
}

func TestJoinRefusesAliasEqualToHost(t *testing.T) {
	// ssh would take the alias's own block for the box (names compare without case).
	for _, args := range [][]string{{"shop"}, {"Shop.example.test", "--alias", "shop.example.test"}} {
		r := runJoin(t, homeWithKeys(t, "id_ed25519"), joinCase{}, args...)
		want := "join: " + args[0] + ": the alias must differ from the host; pass --alias NAME\n" + joinUsage + "\n"
		if r.code != 2 || r.out != "" || r.errOut != want || len(r.f.Calls) != 0 {
			t.Fatalf("%v: %d %q %v", args, r.code, r.errOut, r.f.Calls)
		}
	}
}

func TestJoinServedFileKey(t *testing.T) {
	// id_ed25519 is not on GitHub, work is: work is chosen.
	home := homeWithKeys(t, "id_ed25519", "work")
	work := filepath.Join(home, ".ssh", "work")
	r := runJoin(t, home, joinCase{gh: "alice-gh", served: "ssh-ed25519 AAAAwork\nssh-rsa AAAAother\n"}, "shop.example.test")
	if r.code != 0 || r.out != "connected: alice@shop\n" || r.errOut != "wrote Host shop-via-host to ~/.ssh/config\nwrote Host shop to ~/.ssh/config\n" {
		t.Fatalf("%d %q %q", r.code, r.out, r.errOut)
	}
	if calledPrefix(r.f, "gh", "ssh-key") {
		t.Fatal("touched GitHub keys", r.f.Calls)
	}
	if !called(r.f, jumpCall(work, "shop.example.test")...) || !strings.Contains(sshConfig(t, home), "  IdentityFile "+work+"\n  IdentitiesOnly yes\n") {
		t.Fatal(r.f.Calls, sshConfig(t, home))
	}
}

func TestJoinServedAgentKey(t *testing.T) {
	// A key only in the agent: no -i, IdentityFile or IdentitiesOnly.
	home := homeWithKeys(t, "id_ed25519")
	r := runJoin(t, home, joinCase{agent: "ssh-ed25519 AAAAagent card\n", gh: "alice-gh", served: "ssh-ed25519 AAAAagent\n"}, "shop.example.test")
	cfg := sshConfig(t, home)
	if r.code != 0 || calledPrefix(r.f, "gh", "ssh-key") || !called(r.f, sshBatch("-l", "hades", "shop.example.test")...) ||
		strings.Contains(cfg, "Identit") || !strings.Contains(cfg, "  ProxyCommand ssh -W %h:%p -l hades -o StrictHostKeyChecking=accept-new shop.example.test\n") {
		t.Fatalf("%d %q %v\n%s", r.code, r.errOut, r.f.Calls, cfg)
	}
}

func TestJoinGitHubOverride(t *testing.T) {
	// gh is absent, --github names the account: the served check still runs.
	home := homeWithKeys(t, "id_ed25519")
	r := runJoin(t, home, joinCase{served: "ssh-ed25519 AAAAid_ed25519\n"}, "shop.example.test", "--github", "alice-gh")
	if r.code != 0 || strings.Contains(r.errOut, "github:") {
		t.Fatalf("%d %q", r.code, r.errOut)
	}
}

func TestJoinUploads(t *testing.T) {
	home := homeWithKeys(t, "a", "id_ed25519")
	pub := filepath.Join(home, ".ssh", "id_ed25519.pub")
	r := runJoin(t, home, joinCase{gh: "alice-gh", sshErr: refused}, "203.0.113.10", "--as", "bob", "--alias", "shop")
	if !called(r.f, "gh", "ssh-key", "add", pub, "--title", "hades shop") {
		t.Fatal(r.f.Calls)
	}
	want := "not authorised yet: the box does not accept your key.\n" +
		"Your key is on GitHub as alice-gh: if the administrator has added you, the box picks it up within the hour (they can run: sudo hades-keys bob); otherwise ask them to add you with github: alice-gh.\n" +
		"Then run again: hades join 203.0.113.10 --as bob --alias shop\n"
	if r.code != 2 || r.out != want || !strings.Contains(r.errOut, refused) {
		t.Fatalf("%d %q %q", r.code, r.out, r.errOut)
	}
}

func TestJoinScopeError(t *testing.T) {
	home := homeWithKeys(t, "id_ed25519")
	r := runJoin(t, home, joinCase{gh: "alice-gh", addErr: "HTTP 404: needs the \"admin:public_key\" scope", sshErr: refused}, "shop.example.test")
	if r.code != 2 || !strings.Contains(r.errOut, "github: gh lacks the admin:public_key scope; your key was not added. Run gh auth refresh -h github.com -s admin:public_key, then hades join again.\n") {
		t.Fatalf("%d %q", r.code, r.errOut)
	}
	if !strings.Contains(r.out, "Your key is not on GitHub") {
		t.Fatal(r.out)
	}
}

func TestJoinOtherAddError(t *testing.T) {
	home := homeWithKeys(t, "id_ed25519")
	r := runJoin(t, home, joinCase{gh: "alice-gh", addErr: "boom"}, "shop.example.test")
	if r.code != 0 || !strings.Contains(r.errOut, "github: ") || !strings.Contains(r.errOut, "boom; your key was not added.\n") {
		t.Fatalf("%d %q", r.code, r.errOut)
	}
}

func TestJoinNotOnGitHub(t *testing.T) {
	home := homeWithKeys(t, "id_ed25519")
	r := runJoin(t, home, joinCase{sshErr: refused}, "shop.example.test")
	want := "not authorised yet: the box does not accept your key.\n" +
		"Your key is not on GitHub: send the key below to the administrator, or add it at https://github.com/settings/keys.\n" +
		"ssh-ed25519 AAAAid_ed25519 alice@laptop\n" +
		"Then run again: hades join shop.example.test\n"
	if r.code != 2 || r.out != want || !strings.Contains(r.errOut, notLoggedIn) {
		t.Fatalf("%d %q %q", r.code, r.out, r.errOut)
	}
}

func TestJoinUnknownGitHubLogin(t *testing.T) {
	home := homeWithKeys(t, "id_ed25519")
	r := runJoin(t, home, joinCase{}, "shop.example.test", "--github", "nobody")
	if r.code != 0 || !strings.Contains(r.errOut, "github: GET ") || !strings.Contains(r.errOut, "404 Not Found\n") || strings.Contains(r.errOut, notLoggedIn) {
		t.Fatalf("%d %q", r.code, r.errOut)
	}
}

func TestJoinOtherGhAccount(t *testing.T) {
	// gh is logged in, but as another account than --github names: no upload.
	home := homeWithKeys(t, "id_ed25519")
	r := runJoin(t, home, joinCase{gh: "other", sshErr: refused}, "shop.example.test", "--github", "alice-gh")
	want := "github: gh is logged in as other, not alice-gh; your key was not added. Add the key below at https://github.com/settings/keys.\n"
	if r.code != 2 || !strings.Contains(r.errOut, want) || strings.Contains(r.errOut, notLoggedIn) || calledPrefix(r.f, "gh", "ssh-key") {
		t.Fatalf("%d %q %v", r.code, r.errOut, r.f.Calls)
	}
}

func TestJoinUnreachable(t *testing.T) {
	home := homeWithKeys(t, "id_ed25519")
	r := runJoin(t, home, joinCase{sshErr: "ssh: Could not resolve hostname shop: nodename nor servname provided\n"}, "shop.example.test")
	if r.code != 1 || r.out != "" || !strings.HasSuffix(r.errOut, "join: ssh: ssh: Could not resolve hostname shop: nodename nor servname provided\n") {
		t.Fatalf("%d %q %q", r.code, r.out, r.errOut)
	}
}

func TestJoinHandOffExit(t *testing.T) {
	// Without a passed hades-onboard, the tailnet is not asked and <alias> is not written.
	home := homeWithKeys(t, "id_ed25519")
	for _, tc := range []struct {
		handoff, code int
		out           string
	}{
		{127, 0, "connected: alice@shop\nnext: ssh shop-via-host\n"},
		{1, 1, "connected: alice@shop\n"},
	} {
		r := runJoin(t, home, joinCase{handoff: tc.handoff, status: tsRunning}, "shop.example.test")
		if r.code != tc.code || r.out != tc.out || calledPrefix(r.f, sshBatch("shop-via-host", "tailscale")...) || strings.Contains(sshConfig(t, home), "Host shop\n") {
			t.Fatalf("%d: %d %q %v\n%s", tc.handoff, r.code, r.out, r.f.Calls, sshConfig(t, home))
		}
	}
}

func TestJoinGeneratesKey(t *testing.T) {
	home := t.TempDir()
	r := runJoin(t, home, joinCase{}, "shop.example.test", "--as", "bob")
	hostname, _ := os.Hostname()
	key := filepath.Join(home, ".ssh", "id_ed25519")
	if r.code != 0 || !called(r.f, "ssh-keygen", "-t", "ed25519", "-N", "", "-f", key, "-C", "alice@"+hostname) || !strings.HasPrefix(r.errOut, "generated "+key+"\n") {
		t.Fatalf("%d %q %v", r.code, r.errOut, r.f.Calls)
	}
	if !strings.Contains(sshConfig(t, home), "IdentityFile "+key+"\n") {
		t.Fatal(sshConfig(t, home))
	}
}
