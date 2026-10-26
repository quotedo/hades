package naming

import (
	"crypto/sha256"
	"encoding/hex"
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
	if _, err := Slug("???"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatal(err)
	}
	if _, err := Slug(strings.Repeat("a", 31)); err == nil || !strings.Contains(err.Error(), "longer than 30") {
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
	if _, err := Label(DefaultLabelTemplate, Vars{Project: "shop", Name: "???"}); err == nil || !strings.Contains(err.Error(), "empty") {
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
	if got := Suffix("shop", "fix-login"); got != "_shop_fix_login" {
		t.Fatal(got)
	}
	if got := Suffix("shop", "feature-x1"); got != "_shop_feature_x1_" {
		t.Fatal(got)
	}
	if got := Suffix("Shop", "Ünïcode!"); got != "_shop_ncode" {
		t.Fatal(got)
	}
	// "_<project>_<name>" is the label "<project>-<name>" with "_" for "-".
	if a, b := Suffix("my-shop", "x"), Suffix("my", "shop-x"); a != "_my_shop_x" || b != a {
		t.Fatal(a, b)
	}
	if a, b := Suffix("my-shop", "x"), Suffix("myshop", "x"); a == b {
		t.Fatalf("both %q", a)
	}
	// 31 bytes ending in a digit: the trailing "_" makes 32, no hash.
	if got := Suffix("shop", strings.Repeat("a", 24)+"1"); got != "_shop_"+strings.Repeat("a", 24)+"1_" {
		t.Fatal(got)
	}
	// 32 bytes ending in a digit would be 33 with the "_": hashed.
	edge := "_shop_" + strings.Repeat("a", 25) + "1"
	if got := Suffix("shop", strings.Repeat("a", 25)+"1"); got != hashedSuffix(edge) {
		t.Fatalf("%q want %q", got, hashedSuffix(edge))
	}

	// Long names: one whose hash ends in a letter (31 bytes) and one whose
	// hash ends in a digit (32 bytes, trailing "_").
	var letter, digit bool
	for i := 0; !(letter && digit) && i < 1000; i++ {
		name := strings.Repeat("x", 40) + strconv.Itoa(i) + "y"
		full := "_shop_" + name
		got, want := Suffix("shop", name), hashedSuffix(full)
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
		if err := CheckPlain(l); err == nil || !strings.Contains(err.Error(), "plain file name") {
			t.Errorf("%q: %v", l, err)
		}
	}
	for _, l := range []string{"demo-feature-x", "a", "..a", "a.b"} {
		if err := CheckPlain(l); err != nil {
			t.Errorf("%q: %v", l, err)
		}
	}
}
