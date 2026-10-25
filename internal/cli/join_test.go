package cli

import (
	"bytes"
	"os"
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

func TestFindPublicKey(t *testing.T) {
	home := t.TempDir()
	if _, ok := findPublicKey(home); ok {
		t.Fatal("found a key in an empty home")
	}
	ssh := filepath.Join(home, ".ssh")
	os.MkdirAll(ssh, 0o700)
	for _, f := range []string{"work.pub", "id_rsa.pub", "id_rsa"} {
		os.WriteFile(filepath.Join(ssh, f), []byte("k"), 0o600)
	}
	if p, ok := findPublicKey(home); !ok || p != filepath.Join(ssh, "id_rsa.pub") {
		t.Fatalf("first *.pub: %q %v", p, ok)
	}
	os.WriteFile(filepath.Join(ssh, "id_ed25519.pub"), []byte("k"), 0o600)
	if p, ok := findPublicKey(home); !ok || p != filepath.Join(ssh, "id_ed25519.pub") {
		t.Fatalf("id_ed25519.pub first: %q %v", p, ok)
	}
}

func TestUpsertSSHConfigKeepsOtherContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ssh", "config")
	upsertSSHConfig := func(path, alias, block string) (bool, error) {
		return envfile.UpsertBlock(path, "# hades:begin "+alias+"\n", "# hades:end "+alias+"\n", block)
	}
	changed, err := upsertSSHConfig(path, "shop", sshConfigBlock("shop", "shop.example.test", "alice"))
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
	block := "# hades:begin shop\nHost shop\n  HostName shop.example.test\n  User alice\n  ForwardAgent yes\n# hades:end shop\n"
	os.WriteFile(path, []byte(before+block+after), 0o644)
	os.Chmod(path, 0o644)

	changed, err = upsertSSHConfig(path, "shop", sshConfigBlock("shop", "shop.example.test", "alice"))
	if err != nil || changed {
		t.Fatalf("identical rerun: %v %v", changed, err)
	}
	changed, err = upsertSSHConfig(path, "shop", sshConfigBlock("shop", "203.0.113.10", "alice"))
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
	if _, err := upsertSSHConfig(path, "sh", sshConfigBlock("sh", "sh.example.test", "alice")); err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, path))
	if !strings.HasPrefix(got, want+"\n# hades:begin sh\nHost sh\n") || strings.Count(got, "# hades:begin") != 2 {
		t.Fatalf("second alias:\n%s", got)
	}
}

// joinFake answers gh (logged in when ghOK, with the key already listed when
// listed) and ssh (exiting sshExit).
func joinFake(ghOK, listed bool, sshExit int) *execx.Fake {
	return &execx.Fake{Handle: func(c execx.Cmd) (execx.Result, error) {
		fail := func(code int) (execx.Result, error) {
			r := execx.Result{ExitCode: code, Stderr: "denied"}
			return r, &execx.ExitError{Cmd: c, Result: r}
		}
		a := strings.Join(c.Argv, " ")
		switch {
		case strings.HasPrefix(a, "gh ") && !ghOK:
			return fail(1)
		case a == "gh api user --jq .login":
			return execx.Result{Stdout: "alice-gh\n"}, nil
		case a == "gh ssh-key list" && listed:
			return execx.Result{Stdout: "laptop\tssh-ed25519 AAAAkey\t2026-10-25\t1\tauthentication\n"}, nil
		case c.Argv[0] == "ssh" && sshExit != 0:
			return fail(sshExit)
		}
		return execx.Result{}, nil
	}}
}

func runJoin(t *testing.T, home string, f *execx.Fake, args ...string) (int, string) {
	t.Helper()
	env := map[string]string{"HOME": home, "USER": "alice"}
	var out bytes.Buffer
	ctx := &Context{Stdout: &out, Stderr: &bytes.Buffer{}, Getenv: func(k string) string { return env[k] }, Runner: f}
	return join(ctx, args), out.String()
}

func homeWithKey(t *testing.T) (home, pub string) {
	home = t.TempDir()
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	pub = filepath.Join(home, ".ssh", "id_ed25519.pub")
	os.WriteFile(pub, []byte("ssh-ed25519 AAAAkey alice@laptop\n"), 0o644)
	return home, pub
}

func called(f *execx.Fake, argv ...string) bool {
	return slices.ContainsFunc(f.Calls, func(c execx.Cmd) bool { return slices.Equal(c.Argv, argv) })
}

func TestJoinMessages(t *testing.T) {
	t.Run("connected", func(t *testing.T) {
		home, pub := homeWithKey(t)
		f := joinFake(true, true, 0)
		code, out := runJoin(t, home, f, "shop.example.test")
		if code != 0 || !strings.HasPrefix(out, "connected: alice@shop\n") {
			t.Fatalf("%d %q", code, out)
		}
		if called(f, "gh", "ssh-key", "add", pub, "--title", "hades shop") {
			t.Fatal("added a key GitHub already has")
		}
		if !called(f, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "shop", "true") {
			t.Fatal(f.Calls)
		}
	})
	t.Run("refused names the GitHub login", func(t *testing.T) {
		home, pub := homeWithKey(t)
		f := joinFake(true, false, 255)
		code, out := runJoin(t, home, f, "203.0.113.10", "--as", "bob", "--alias", "shop")
		want := "not authorised yet: ask the administrator to add github: alice-gh (or your key below) to your entry and apply, then run: hades join 203.0.113.10 --as bob --alias shop\n"
		if code != 2 || !strings.HasPrefix(out, want) {
			t.Fatalf("%d %q", code, out)
		}
		if !called(f, "gh", "ssh-key", "add", pub, "--title", "hades shop") {
			t.Fatal(f.Calls)
		}
		cfg := string(mustRead(t, filepath.Join(home, ".ssh", "config")))
		if !strings.Contains(cfg, "Host shop\n  HostName 203.0.113.10\n  User bob\n") {
			t.Fatal(cfg)
		}
	})
	t.Run("no gh prints the key", func(t *testing.T) {
		home, _ := homeWithKey(t)
		code, out := runJoin(t, home, joinFake(false, false, 255), "shop.example.test")
		want := "not authorised yet: ask the administrator to add your key below to your entry and apply, then run: hades join shop.example.test\nssh-ed25519 AAAAkey alice@laptop\n"
		if code != 2 || out != want {
			t.Fatalf("%d %q", code, out)
		}
		if _, err := os.Stat(filepath.Join(home, ".ssh", "config")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("generates a key when none", func(t *testing.T) {
		home := t.TempDir()
		f := joinFake(false, false, 0)
		h := f.Handle
		f.Handle = func(c execx.Cmd) (execx.Result, error) {
			if c.Argv[0] == "ssh-keygen" {
				os.WriteFile(c.Argv[len(c.Argv)-3]+".pub", []byte("ssh-ed25519 AAAAnew alice@laptop\n"), 0o644)
			}
			return h(c)
		}
		code, out := runJoin(t, home, f, "shop.example.test", "--as", "bob")
		hostname, _ := os.Hostname()
		key := filepath.Join(home, ".ssh", "id_ed25519")
		if code != 0 || !called(f, "ssh-keygen", "-t", "ed25519", "-N", "", "-f", key, "-C", "alice@"+hostname) {
			t.Fatalf("%d %q %v", code, out, f.Calls)
		}
	})
	t.Run("IP without alias", func(t *testing.T) {
		code, _ := runJoin(t, t.TempDir(), joinFake(true, true, 0), "203.0.113.10")
		if code != 1 {
			t.Fatal(code)
		}
	})
}
