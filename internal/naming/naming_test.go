package naming

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	cases := map[string]string{"Fix_Login/2": "fix-login-2", "--a--": "a", "???": "", "Alice Dev Server": "alice-dev-server"}
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
	got, err := Label(DefaultLabelTemplate, Vars{Project: "shop", Name: "fix-login"})
	if err != nil || got != "shop-fix-login" {
		t.Fatalf("%q %v", got, err)
	}
	got, _ = Label("{{project}}-{{user}}-{{name}}", Vars{Project: "shop", User: "alice", Name: "fix-login"})
	if got != "shop-alice-fix-login" {
		t.Fatal(got)
	}
	long, _ := Label(DefaultLabelTemplate, Vars{Project: "shop", Name: strings.Repeat("a", 70)})
	if len(long) != 63 || !strings.HasPrefix(long, "shop-") {
		t.Fatal(long)
	}
	cut, _ := Label(DefaultLabelTemplate, Vars{Project: "shop", Name: strings.Repeat("a", 57) + "-b"})
	if strings.HasSuffix(cut, "-") {
		t.Fatal(cut)
	}
	if _, err := Label("{{bogus}}", Vars{}); err == nil {
		t.Fatal("unknown placeholder accepted")
	}
	if _, err := Label(DefaultLabelTemplate, Vars{Project: "shop", Name: "???"}); !errors.Is(err, ErrEmptyName) {
		t.Fatal(err)
	}
}

// hashedSuffix is the expected hashed form of a too-long suffix full.
func hashedSuffix(full string) string {
	sum := sha256.Sum256([]byte(full))
	s := full[:22] + "_" + hex.EncodeToString(sum[:])[:8]
	if c := s[len(s)-1]; c >= '0' && c <= '9' {
		s += "_"
	}
	return s
}

func TestSuffix(t *testing.T) {
	if got := Suffix("alice", "fix-login"); got != "_alice_fix_login" {
		t.Fatal(got)
	}
	if got := Suffix("alice", "feature-x1"); got != "_alice_feature_x1_" {
		t.Fatal(got)
	}
	if got := Suffix("Alice", "Ünïcode!"); got != "_alice_ncode" {
		t.Fatal(got)
	}
	// The user part keeps only [a-z0-9], so the first segment is the login.
	if got := Suffix("al.ice-b_x", "a"); got != "_alicebx_a" {
		t.Fatal(got)
	}
	if a, b := Suffix("alice-feature", "x"), Suffix("alice", "feature/x"); a == b {
		t.Fatalf("both %q", a)
	}
	if a, b := Suffix("alice_feature", "x"), Suffix("alice", "feature_x"); a == b {
		t.Fatalf("both %q", a)
	}
	// 31 bytes ending in a digit: the trailing "_" makes 32, no hash.
	if got := Suffix("alice", strings.Repeat("a", 23)+"1"); got != "_alice_"+strings.Repeat("a", 23)+"1_" {
		t.Fatal(got)
	}
	// 32 bytes ending in a digit would be 33 with the "_": hashed.
	edge := "_alice_" + strings.Repeat("a", 24) + "1"
	if got := Suffix("alice", strings.Repeat("a", 24)+"1"); got != hashedSuffix(edge) {
		t.Fatalf("%q want %q", got, hashedSuffix(edge))
	}

	// Long names: one whose hash ends in a letter (31 bytes) and one whose
	// hash ends in a digit (32 bytes, trailing "_").
	var letter, digit bool
	for i := 0; !(letter && digit) && i < 1000; i++ {
		name := strings.Repeat("x", 40) + strconv.Itoa(i) + "y"
		full := "_alice_" + name
		got, want := Suffix("alice", name), hashedSuffix(full)
		if got != want {
			t.Fatalf("%q want %q", got, want)
		}
		switch len(got) {
		case 31:
			letter = true
		case 32:
			digit = true
			if !strings.HasSuffix(got, "_") {
				t.Fatal(got)
			}
		default:
			t.Fatalf("%q: length %d", got, len(got))
		}
	}
	if !letter || !digit {
		t.Fatal("did not cover both hash endings")
	}
}

func TestCheckPlain(t *testing.T) {
	for _, l := range []string{"", ".", "..", "../x", "a/b", "/abs", "x/", "../../../../escape"} {
		if err := CheckPlain(l); !errors.Is(err, ErrNotPlain) {
			t.Errorf("%q: %v", l, err)
		}
	}
	for _, l := range []string{"demo-feature-x", "a", "..a", "a.b"} {
		if err := CheckPlain(l); err != nil {
			t.Errorf("%q: %v", l, err)
		}
	}
}
