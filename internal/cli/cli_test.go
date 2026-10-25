package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/version"
)

// TestMain keeps every test away from this machine's host config.
func TestMain(m *testing.M) {
	config.DefaultHostPath = "/nonexistent/hades/host.toml"
	os.Exit(m.Run())
}

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

func TestGCOwnerFlagIsGone(t *testing.T) {
	if code, _, errs := runMain(func(string) string { return "" }, "gc", "--owner", "bob"); code != 2 {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}
