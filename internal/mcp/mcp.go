// Package mcp points the agent MCP config files committed in a worktree at
// that worktree's own Tidewave port, and hides the edits from git.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/quotedo/hades/internal/execx"
)

// Targets says what to wire. An empty TidewaveURL skips the Tidewave edits;
// an empty ChromePath skips the chrome-devtools entry.
type Targets struct {
	TidewaveURL string
	ChromePath  string
	Log         io.Writer // receives ignored git failures; nil discards
}

type file struct {
	rel  string
	edit func(path string, t Targets) error
}

// files lists the config files in the order Rewire visits them.
var files = []file{
	{".mcp.json", editJSON(func(m map[string]any, t Targets) error {
		return editServers(m, func(s map[string]any) {
			if t.TidewaveURL != "" {
				s["tidewave"] = map[string]any{"type": "http", "url": t.TidewaveURL}
			}
			if t.ChromePath != "" {
				s["chrome-devtools"] = map[string]any{
					"command": "npx",
					"args":    []any{"-y", "chrome-devtools-mcp@latest", "--headless", "--isolated", "--executablePath", t.ChromePath},
				}
			}
		})
	})},
	{".codex/config.toml", editCodex},
	{".gemini/settings.json", editTidewaveServer(func(u string) any { return map[string]any{"httpUrl": u} })},
	{".cursor/mcp.json", editTidewaveServer(func(u string) any { return map[string]any{"url": u} })},
	{".grok/settings.json", editJSON(editGrok)},
}

// Rewire edits the config files that exist in worktree and returns their
// paths relative to it. Each edited file is marked skip-worktree; a failure
// of that git call (e.g. the file is untracked) is logged and ignored.
func Rewire(ctx context.Context, worktree string, t Targets, run execx.Runner) ([]string, error) {
	var edited []string
	for _, f := range files {
		if !needed(f.rel, t) {
			continue
		}
		path := filepath.Join(worktree, f.rel)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return edited, err
		}
		if err := f.edit(path, t); err != nil {
			return edited, fmt.Errorf("mcp: %s: %w", f.rel, err)
		}
		edited = append(edited, f.rel)
		_, err := run.Run(ctx, execx.Cmd{Argv: []string{"git", "-C", worktree, "update-index", "--skip-worktree", f.rel}})
		if err != nil && t.Log != nil {
			fmt.Fprintf(t.Log, "mcp: skip-worktree %s: %v\n", f.rel, err)
		}
	}
	return edited, nil
}

// needed reports whether t asks for any edit of the file.
func needed(rel string, t Targets) bool {
	if rel == ".mcp.json" {
		return t.TidewaveURL != "" || t.ChromePath != ""
	}
	return t.TidewaveURL != ""
}

// editTidewaveServer builds an editor that sets mcpServers.tidewave.
func editTidewaveServer(entry func(url string) any) func(string, Targets) error {
	return editJSON(func(m map[string]any, t Targets) error {
		return editServers(m, func(s map[string]any) { s["tidewave"] = entry(t.TidewaveURL) })
	})
}

// editServers applies fn to the mcpServers object, creating it when absent.
func editServers(m map[string]any, fn func(map[string]any)) error {
	s, ok := m["mcpServers"].(map[string]any)
	if !ok {
		if v, present := m["mcpServers"]; present && v != nil {
			return fmt.Errorf("mcpServers is %T, want object", v)
		}
		s = map[string]any{}
		m["mcpServers"] = s
	}
	fn(s)
	return nil
}

// editGrok treats mcpServers as an array: it drops stale tidewave entries and
// appends a fresh one.
func editGrok(m map[string]any, t Targets) error {
	list := []any{}
	if v, present := m["mcpServers"]; present && v != nil {
		l, ok := v.([]any)
		if !ok {
			return fmt.Errorf("mcpServers is %T, want array", v)
		}
		list = l
	}
	kept := make([]any, 0, len(list)+1)
	for _, e := range list {
		if o, ok := e.(map[string]any); ok && o["name"] == "tidewave" {
			continue
		}
		kept = append(kept, e)
	}
	kept = append(kept, map[string]any{
		"name":      "tidewave",
		"transport": map[string]any{"type": "http", "url": t.TidewaveURL},
	})
	m["mcpServers"] = kept
	return nil
}

func editJSON(fn func(map[string]any, Targets) error) func(string, Targets) error {
	return func(path string, t Targets) error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		m := map[string]any{}
		if err := json.Unmarshal(b, &m); err != nil {
			return err
		}
		if m == nil {
			return fmt.Errorf("top level is null, want object")
		}
		if err := fn(m, t); err != nil {
			return err
		}
		out, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return err
		}
		return writeAtomic(path, append(out, '\n'))
	}
}

var urlLine = regexp.MustCompile(`^\s*url\s*=`)

const codexHeader = "[mcp_servers.tidewave]"

// editCodex edits the file line by line so every other byte is preserved.
func editCodex(path string, t Targets) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return writeAtomic(path, []byte(setCodexURL(string(b), t.TidewaveURL)))
}

func setCodexURL(src, url string) string {
	newLine := `url = "` + url + `"`
	lines := strings.SplitAfter(src, "\n")
	header := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == codexHeader {
			header = i
			break
		}
	}
	if header < 0 {
		if src != "" && !strings.HasSuffix(src, "\n") {
			src += "\n"
		}
		return src + "\n" + codexHeader + "\n" + newLine + "\n"
	}
	for i := header + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimLeft(lines[i], " \t"), "[") {
			break
		}
		if urlLine.MatchString(lines[i]) {
			lines[i] = newLine + lineEnding(lines[i])
			return strings.Join(lines, "")
		}
	}
	if !strings.HasSuffix(lines[header], "\n") {
		lines[header] += "\n"
	}
	lines[header] += newLine + "\n"
	return strings.Join(lines, "")
}

func lineEnding(l string) string {
	switch {
	case strings.HasSuffix(l, "\r\n"):
		return "\r\n"
	case strings.HasSuffix(l, "\n"):
		return "\n"
	}
	return ""
}

// writeAtomic replaces path via a temp file and rename, keeping its mode.
func writeAtomic(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hades-mcp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
