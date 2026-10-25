package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/version"
)

func TestMainVersion(t *testing.T) {
	var out, errb bytes.Buffer
	code := Main([]string{"version"}, &out, &errb, func(string) string { return "" })
	if code != 0 || strings.TrimSpace(out.String()) != version.Version {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}

func TestMainUnknownCommandIsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	code := Main([]string{"bogus"}, &out, &errb, func(string) string { return "" })
	if code != 2 || !strings.Contains(errb.String(), "usage:") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
}

func TestHelpPrintsUsageAndExitsZero(t *testing.T) {
	argvs := [][]string{{"--help"}, {"-h"}}
	for name := range commands {
		argvs = append(argvs, []string{name, "--help"}, []string{name, "-h"})
	}
	argvs = append(argvs, []string{"join", "shop", "--help"})
	for _, args := range argvs {
		var out, errb bytes.Buffer
		code := Main(args, &out, &errb, func(string) string { return "" })
		if code != 0 || !strings.HasPrefix(out.String(), "usage: hades") || errb.Len() != 0 {
			t.Errorf("%v: code=%d out=%q err=%q", args, code, out.String(), errb.String())
		}
	}
}

func TestGlobalFlagsBeforeAndAfterCommand(t *testing.T) {
	for _, args := range [][]string{{"--json", "version"}, {"version", "--json"}} {
		var out, errb bytes.Buffer
		if code := Main(args, &out, &errb, func(string) string { return "" }); code != 0 {
			t.Fatalf("%v: code=%d err=%q", args, code, errb.String())
		}
	}
}

func TestCaddyReloadRefusesLaptopMode(t *testing.T) {
	var out, errb bytes.Buffer
	getenv := func(k string) string {
		switch k {
		case "HADES_HOST_CONFIG":
			return "laptop"
		case "HOME":
			return t.TempDir()
		}
		return ""
	}
	code := Main([]string{"caddy-reload"}, &out, &errb, getenv)
	if code != 1 || !strings.Contains(errb.String(), "caddy-reload: no host config; nothing to reload") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
}

func TestCaddyReloadRejectsPositionalArgs(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Main([]string{"caddy-reload", "extra"}, &out, &errb, func(string) string { return "" }); code != 2 || !strings.Contains(errb.String(), "usage") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
}

func TestLoadReloadConfigRootIgnoresEnvAndChecksFile(t *testing.T) {
	// Not root-owned (we are not root in tests), so the root branch must refuse
	// even though HADES_HOST_CONFIG points at a usable file.
	path := filepath.Join(t.TempDir(), "host.toml")
	os.WriteFile(path, []byte("x"), 0o644)
	getenv := func(k string) string {
		if k == "HADES_HOST_CONFIG" {
			return "laptop"
		}
		return ""
	}
	_, _, err := loadReloadConfig(0, getenv, path)
	if os.Geteuid() != 0 && (err == nil || !strings.Contains(err.Error(), "not owned by root")) {
		t.Fatalf("%v", err)
	}
	if _, _, err := loadReloadConfig(0, getenv, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing root config accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(path, link)
	if _, _, err := loadReloadConfig(0, getenv, link); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("%v", err)
	}
	// Non-root keeps config.Load behaviour: laptop override gives Laptop mode.
	if _, mode, err := loadReloadConfig(501, getenv, path); err != nil || mode != config.Laptop {
		t.Fatalf("%v %v", mode, err)
	}
}
