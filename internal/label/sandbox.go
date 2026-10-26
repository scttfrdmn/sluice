// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package label

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/scttfrdmn/sluice/internal/config"
)

// Language support for the code rungs. Test-run languages get rung 1;
// compile-only languages get rung 2; anything else falls to the judges.
var (
	testLangs    = map[string]bool{"python": true, "go": true}
	compileLangs = map[string]bool{"python": true, "go": true, "javascript": true, "bash": true}
	langAlias    = map[string]string{"py": "python", "python3": "python", "golang": "go", "js": "javascript", "node": "javascript", "sh": "bash", "shell": "bash", "zsh": "bash"}
)

// NormLang maps a fence or hint language to a canonical name.
func NormLang(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if a, ok := langAlias[s]; ok {
		return a
	}
	return s
}

var reFence = regexp.MustCompile("(?s)```([A-Za-z0-9_+#-]*)[^\n]*\n(.*?)```")

// FirstCodeBlock returns the language and body of the first fenced code
// block in s.
func FirstCodeBlock(s string) (lang, code string, ok bool) {
	m := reFence.FindStringSubmatch(s)
	if m == nil {
		return "", "", false
	}
	return NormLang(m[1]), m[2], true
}

// RunResult is one sandbox execution.
type RunResult struct {
	Compiled bool
	Passed   bool // only meaningful when a test was run
	TimedOut bool
	Output   string
}

// Sandbox compiles and optionally tests a solution.
type Sandbox interface {
	// Run compiles solution in lang; if test is non-empty it also runs it.
	Run(ctx context.Context, lang, solution, test string) (RunResult, error)
}

// NewSandbox returns the configured sandbox.
func NewSandbox(cfg *config.Config) (Sandbox, error) {
	switch cfg.Label.Sandbox {
	case "docker":
		return &Docker{images: cfg.Label.Images, timeout: time.Duration(cfg.Label.TimeoutSeconds) * time.Second}, nil
	case "fake":
		return FakeSandbox{}, nil
	}
	return nil, fmt.Errorf("unknown sandbox %q", cfg.Label.Sandbox)
}

// Docker runs code in a throwaway container with no network, a memory and
// pid cap, and a wall-clock timeout (CC_PROMPT.md cmd/label).
type Docker struct {
	images  map[string]string
	timeout time.Duration
}

var rePackage = regexp.MustCompile(`(?m)^package\s+\w+`)

func (d *Docker) Run(ctx context.Context, lang, solution, test string) (RunResult, error) {
	image, ok := d.images[lang]
	if !ok {
		return RunResult{}, fmt.Errorf("no image for %s", lang)
	}
	// Small, short-lived files; the system temp dir is shared with Docker
	// Desktop by default, unlike the external drive.
	root := filepath.Join(os.TempDir(), "sluice-sandbox")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return RunResult{}, err
	}
	work, err := os.MkdirTemp(root, "run-")
	if err != nil {
		return RunResult{}, err
	}
	defer os.RemoveAll(work)

	var files map[string]string
	var compile, run string
	switch lang {
	case "python":
		files = map[string]string{"solution.py": solution}
		compile = "python -m py_compile solution.py"
		if test != "" {
			files["test_solution.py"] = test
			run = "python test_solution.py"
		}
	case "go":
		src := solution
		if rePackage.MatchString(src) {
			src = rePackage.ReplaceAllString(src, "package solution")
		} else {
			src = "package solution\n\n" + src
		}
		files = map[string]string{"go.mod": "module solution\n\ngo 1.26\n", "solution.go": src}
		compile = "go build ./..."
		if test != "" {
			files["solution_test.go"] = test
			run = "go test ./..."
		}
	case "javascript":
		files = map[string]string{"solution.js": solution}
		compile = "node --check solution.js"
	case "bash":
		files = map[string]string{"solution.sh": solution}
		compile = "bash -n solution.sh"
	default:
		return RunResult{}, fmt.Errorf("unsupported language %s", lang)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(work, name), []byte(body), 0o644); err != nil {
			return RunResult{}, err
		}
	}
	// Exit code 10 = compile failure, otherwise the test's own status.
	script := "(" + compile + ") || exit 10"
	if run != "" {
		script += "; " + run
	}
	name := "sluice-" + randHex()
	args := []string{"run", "--rm", "--name", name, "--network", "none", "--memory", "512m", "--pids-limit", "256",
		"--cpus", "1", "-e", "GOFLAGS=-mod=mod", "-e", "CGO_ENABLED=0", "-e", "HOME=/tmp",
		"-v", work + ":/w", "-w", "/w", image, "sh", "-c", script}
	cctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "docker", args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	res := RunResult{Output: clip(out.String(), 2000)}
	if cctx.Err() == context.DeadlineExceeded {
		exec.Command("docker", "rm", "-f", name).Run()
		// A timeout fails the cell whether it hit compile or test.
		res.TimedOut = true
		return res, nil
	}
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		return res, err // docker itself failed
	}
	switch {
	case code == 125 || code == 126 || code == 127:
		return res, fmt.Errorf("docker run failed (exit %d): %s", code, res.Output)
	case code == 10:
		res.Compiled = false
	default:
		res.Compiled = true
		res.Passed = run != "" && code == 0
	}
	return res, nil
}

func randHex() string {
	var b [6]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// FakeSandbox reads the fake backend's markers. Used only by `make e0`.
type FakeSandbox struct{}

func (FakeSandbox) Run(_ context.Context, lang, solution, test string) (RunResult, error) {
	if !compileLangs[lang] {
		return RunResult{}, fmt.Errorf("unsupported language %s", lang)
	}
	r := RunResult{Compiled: !strings.Contains(solution, "[[fake:nocompile]]")}
	if test != "" && r.Compiled {
		r.Passed = strings.Contains(solution, "[[fake:pass]]")
	}
	return r, nil
}
