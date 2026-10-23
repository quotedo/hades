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

const (
	envWorktreeHeader = "# written by hades; do not edit"
	blockBegin        = "# hades:begin"
	blockEnd          = "# hades:end"
)

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

// ReadEnvWorktree parses a file written by WriteEnvWorktree. A missing file
// yields an empty map.
func ReadEnvWorktree(path string) (map[string]string, error) {
	m := map[string]string{}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
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
		m[k] = v
	}
	return m, nil
}

// UpdateEnvrc replaces the hades-managed block in path with export lines for
// kv, appending the block when absent. Everything outside the block is kept
// byte for byte, and the file is not rewritten when nothing changes.
func UpdateEnvrc(path string, kv []KV) error {
	var b strings.Builder
	b.WriteString(blockBegin + "\n")
	for _, e := range kv {
		b.WriteString("export " + e.Key + "=" + shellQuote(e.Value) + "\n")
	}
	b.WriteString(blockEnd + "\n")
	block := b.String()

	cur, err := os.ReadFile(path)
	missing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missing {
		return err
	}
	content := string(cur)

	var next string
	if start, end, ok, err := findBlock(content); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	} else if ok {
		next = content[:start] + block + content[end:]
	} else {
		next = content
		if next != "" && !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		next += block
	}

	if !missing && next == content {
		return nil
	}
	// The block holds DB_PASSWORD and SECRET_KEY_BASE, so a new file is
	// private like .env.worktree; an existing file keeps its mode.
	return os.WriteFile(path, []byte(next), 0o600)
}

// findBlock returns the byte range of the managed block, from the start of the
// begin line through the end of the end line (including its newline).
func findBlock(content string) (start, end int, ok bool, err error) {
	start = -1
	pos := 0
	for pos < len(content) {
		lineEnd := len(content)
		next := len(content)
		if i := strings.IndexByte(content[pos:], '\n'); i >= 0 {
			lineEnd = pos + i
			next = lineEnd + 1
		}
		switch content[pos:lineEnd] {
		case blockBegin:
			if start < 0 {
				start = pos
			}
		case blockEnd:
			if start >= 0 {
				return start, next, true, nil
			}
		}
		pos = next
	}
	if start >= 0 {
		return 0, 0, false, fmt.Errorf("%q without matching %q", blockBegin, blockEnd)
	}
	return 0, 0, false, nil
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
