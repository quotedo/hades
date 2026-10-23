package cli

import (
	"bytes"
	"strings"
	"testing"

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
