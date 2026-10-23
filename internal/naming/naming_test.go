package naming

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	cases := map[string]string{"Fix_Login/2": "fix-login-2", "--a--": "a", "???": "", "Roman Dev Server": "roman-dev-server"}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSlugErrors(t *testing.T) {
	if _, err := Slug("???"); !errors.Is(err, ErrEmptyName) {
		t.Fatal(err)
	}
	if _, err := Slug(strings.Repeat("a", 31)); !errors.Is(err, ErrSlugTooLong) {
		t.Fatal(err)
	}
}

func TestLabel(t *testing.T) {
	got, err := Label(DefaultLabelTemplate, Vars{Project: "jarl", Name: "fix-login"})
	if err != nil || got != "jarl-fix-login" {
		t.Fatalf("%q %v", got, err)
	}
	got, _ = Label("{{project}}-{{user}}-{{name}}", Vars{Project: "jarl", User: "roman", Name: "fix-login"})
	if got != "jarl-roman-fix-login" {
		t.Fatal(got)
	}
	long, _ := Label(DefaultLabelTemplate, Vars{Project: "jarl", Name: strings.Repeat("a", 70)})
	if len(long) != 63 || !strings.HasPrefix(long, "jarl-") {
		t.Fatal(long)
	}
	cut, _ := Label(DefaultLabelTemplate, Vars{Project: "jarl", Name: strings.Repeat("a", 57) + "-b"})
	if strings.HasSuffix(cut, "-") {
		t.Fatal(cut)
	}
	if _, err := Label("{{bogus}}", Vars{}); err == nil {
		t.Fatal("unknown placeholder accepted")
	}
	if _, err := Label(DefaultLabelTemplate, Vars{Project: "jarl", Name: "???"}); !errors.Is(err, ErrEmptyName) {
		t.Fatal(err)
	}
}

func TestSuffix(t *testing.T) {
	if got := Suffix("roman", "fix-login"); got != "_roman_fix_login" {
		t.Fatal(got)
	}
	long := Suffix("roman", strings.Repeat("x", 40))
	full := "_roman_" + strings.Repeat("x", 40)
	sum := sha256.Sum256([]byte(full))
	want := full[:23] + "_" + hex.EncodeToString(sum[:])[:8]
	if long != want || len(long) != 32 {
		t.Fatalf("%q want %q", long, want)
	}
	if got := Suffix("Roman", "Ünïcode!"); got != "_roman_ncode" {
		t.Fatal(got)
	}
}
