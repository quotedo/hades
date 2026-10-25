// Package naming derives the identifiers a worktree needs (hostname labels,
// slugs and database suffixes) from user-supplied names.
package naming

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// DefaultLabelTemplate is the hostname label used when none is configured.
const DefaultLabelTemplate = "{{project}}-{{name}}"

const (
	maxSlug   = 30
	maxLabel  = 63
	maxSuffix = 32
	// suffixHead is how many bytes of a too-long suffix survive before the hash.
	suffixHead = 22
	// suffixHash is how many hex characters of the SHA-256 digest are kept.
	suffixHash = 8
)

// errEmptyName means the input has no characters left after sanitising.
var errEmptyName = errors.New("naming: name is empty after sanitising")

// CheckPlain returns an error unless label can name a file inside a registry
// directory: non-empty, a single path element, and not "." or "..". Labels
// read from the registry are untrusted, so every path built from one goes
// through this check first.
func CheckPlain(label string) error {
	if label == "" || label != filepath.Base(label) || label == "." || label == ".." {
		return fmt.Errorf("refusing label %q: must be a plain file name", label)
	}
	return nil
}

var placeholder = regexp.MustCompile(`\{\{([^{}]*)\}\}`)

// Sanitize lower-cases s, turns every run of characters outside [a-z0-9]
// into a single "-", and strips leading and trailing "-".
func Sanitize(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		} else {
			dash = true
		}
	}
	return b.String()
}

// Slug sanitises s and checks that the result is usable as a slug.
func Slug(s string) (string, error) {
	slug := Sanitize(s)
	if slug == "" {
		return "", errEmptyName
	}
	if len(slug) > maxSlug {
		return "", fmt.Errorf("naming: slug %q is longer than %d characters", slug, maxSlug)
	}
	return slug, nil
}

// Vars are the values substituted into a label template.
type Vars struct {
	Project, User, Name string
}

// Label renders tmpl with the sanitised v. When the result is longer than 63
// bytes the name is shortened by the excess. Unknown placeholders are errors.
func Label(tmpl string, v Vars) (string, error) {
	v = Vars{Project: Sanitize(v.Project), User: Sanitize(v.User), Name: Sanitize(v.Name)}
	label, err := render(tmpl, v)
	if err != nil {
		return "", err
	}
	if v.Name == "" {
		return "", errEmptyName
	}
	if excess := len(label) - maxLabel; excess > 0 {
		v.Name = v.Name[:max(0, len(v.Name)-excess)]
		if label, err = render(tmpl, v); err != nil {
			return "", err
		}
	}
	label = strings.TrimRight(label, "-")
	if label == "" {
		return "", errEmptyName
	}
	if len(label) > maxLabel {
		return "", fmt.Errorf("naming: label %q is longer than %d characters", label, maxLabel)
	}
	return label, nil
}

func render(tmpl string, v Vars) (string, error) {
	var bad error
	out := placeholder.ReplaceAllStringFunc(tmpl, func(m string) string {
		switch m[2 : len(m)-2] {
		case "project":
			return v.Project
		case "user":
			return v.User
		case "name":
			return v.Name
		}
		bad = fmt.Errorf("naming: unknown placeholder %s", m)
		return ""
	})
	return out, bad
}

// Suffix builds a database suffix "_<user>_<name>". The user part keeps only
// lower-case letters and digits, so the first "_"-delimited segment of a
// suffix is always the owner's login: "alice-feature" with "x" and "alice"
// with "feature/x" cannot collide. In the name, lower-case letters, digits
// and "_" are kept, "-" becomes "_", and everything else is dropped. A result
// longer than 32 bytes (counting the "_" below) keeps its first 22 bytes and
// appends "_" plus the first 8 hex characters of the SHA-256 of the full
// string. A suffix never ends in a digit: "_" is appended when it would, so
// the digits after <base><suffix> can only be this worktree's
// MIX_TEST_PARTITION.
func Suffix(user, name string) string {
	var b strings.Builder
	b.WriteByte('_')
	for _, c := range []byte(strings.ToLower(user)) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteByte(c)
		}
	}
	b.WriteByte('_')
	for _, c := range []byte(strings.ToLower(name)) {
		switch {
		case c == '-':
			b.WriteByte('_')
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_':
			b.WriteByte(c)
		}
	}
	s := b.String()
	if len(noTrailingDigit(s)) > maxSuffix {
		sum := sha256.Sum256([]byte(s))
		s = s[:suffixHead] + "_" + hex.EncodeToString(sum[:])[:suffixHash]
	}
	return noTrailingDigit(s)
}

// noTrailingDigit appends "_" to s when s ends in a digit.
func noTrailingDigit(s string) string {
	if n := len(s); n > 0 && s[n-1] >= '0' && s[n-1] <= '9' {
		return s + "_"
	}
	return s
}
