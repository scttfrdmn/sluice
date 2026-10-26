// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package harvest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/scttfrdmn/sluice/internal/config"
)

// PullSessions copies every host's ~/.claude/projects/**/*.jsonl into
// dest/<host>/projects/ (CC_PROMPT.md step 0). The local host is copied
// with a local rsync; remote hosts go over ssh. An unreachable host is an
// error, never a silent skip.
func PullSessions(ctx context.Context, cfg *config.Config, dest string, w io.Writer) error {
	var failed []string
	for _, h := range cfg.Hosts {
		out := filepath.Join(dest, h.Name, "projects") + "/"
		if err := os.MkdirAll(out, 0o755); err != nil {
			return err
		}
		var src string
		if h.Local {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			src = filepath.Join(home, ".claude", "projects") + "/"
		} else {
			probe := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", h.Name, "test -d ~/.claude/projects")
			if b, err := probe.CombinedOutput(); err != nil {
				fmt.Fprintf(w, "%-14s UNREACHABLE or no ~/.claude/projects: %v %s\n", h.Name, err, strings.TrimSpace(string(b)))
				failed = append(failed, h.Name)
				continue
			}
			src = h.Name + ":~/.claude/projects/"
		}
		cmd := exec.CommandContext(ctx, "rsync", "-az", "--include=*/", "--include=*.jsonl", "--exclude=*", src, out)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(w, "%-14s rsync FAILED: %v\n", h.Name, err)
			failed = append(failed, h.Name)
			continue
		}
		files, bytes, err := countJSONL(out)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%-14s %6d files  %8.1f MB\n", h.Name, files, float64(bytes)/1e6)
	}
	if len(failed) > 0 {
		return errors.New("pull-sessions failed for: " + strings.Join(failed, ", "))
	}
	return nil
}

func countJSONL(dir string) (files int, bytes int64, err error) {
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
			info, err := d.Info()
			if err != nil {
				return err
			}
			files++
			bytes += info.Size()
		}
		return nil
	})
	return
}
