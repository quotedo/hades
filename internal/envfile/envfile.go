// Package envfile computes the per-worktree environment and writes it to
// .env.worktree (owned by hades) and to a managed block in .envrc.
package envfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// KV is one environment entry; slices of KV keep a stable order.
type KV struct{ Key, Value string }

const envWorktreeHeader = "# written by hades; do not edit"

// WriteEnvWorktree atomically writes kv to path with mode 0600.
func WriteEnvWorktree(path string, kv []KV) error {
	var b strings.Builder
	b.WriteString(envWorktreeHeader + "\n")
	for _, e := range kv {
		v := e.Value
		if strings.ContainsAny(v, " \t\r\n#\"\\") {
			v = strconv.Quote(v)
		}
		b.WriteString(e.Key + "=" + v + "\n")
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".env.worktree-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func(cause error) error {
		tmp.Close()
		os.Remove(tmpName)
		return cause
	}
	if err := tmp.Chmod(0o600); err != nil {
		return cleanup(err)
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// ReadEnvWorktree parses a file written by WriteEnvWorktree, in file order.
// A missing file yields nil.
func ReadEnvWorktree(path string) ([]KV, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var kv []KV
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE", path, i+1)
		}
		if strings.HasPrefix(v, `"`) {
			uq, err := strconv.Unquote(v)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: bad quoted value: %w", path, i+1, err)
			}
			v = uq
		}
		kv = append(kv, KV{k, v})
	}
	return kv, nil
}

// Exports renders kv as shell export lines, one per entry.
func Exports(kv []KV) string {
	var b strings.Builder
	for _, e := range kv {
		b.WriteString("export " + e.Key + "=" + shellQuote(e.Value) + "\n")
	}
	return b.String()
}

// UpdateEnvrc replaces the hades-managed block in path with export lines for
// kv, appending the block when absent.
func UpdateEnvrc(path string, kv []KV) error {
	_, err := UpsertBlock(path, "# hades:begin\n", "# hades:end\n", Exports(kv))
	return err
}

// UpsertBlock writes begin+body+end into path, replacing the block from an
// earlier begin line to the next end line or appending one. Everything else
// is kept byte for byte, and the file is only rewritten when the result
// differs. A new file is 0600 in a 0700 directory (the block may hold
// secrets); an existing file keeps its mode.
func UpsertBlock(path, begin, end, body string) (changed bool, err error) {
	block := begin + body + end
	cur, err := os.ReadFile(path)
	missing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missing {
		return false, err
	}
	s := string(cur)
	next := s
	// The leading "\n" makes the begin marker match at a line start only.
	if i := strings.Index("\n"+s, "\n"+begin); i >= 0 {
		j := strings.Index(s[i:], end)
		if j < 0 {
			return false, fmt.Errorf("%s: %q has no %q line", path, strings.TrimSpace(begin), strings.TrimSpace(end))
		}
		next = s[:i] + block + s[i+j+len(end):]
	} else {
		if next != "" && !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		next += block
	}
	if !missing && next == s {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(next), 0o600)
}

func shellQuote(v string) string {
	safe := v != ""
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_./:@%+=-", r)) {
			safe = false
			break
		}
	}
	if safe {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}
