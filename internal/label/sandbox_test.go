// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package label

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestDockerSandbox runs real containers; enable with SLUICE_DOCKER_TEST=1.
func TestDockerSandbox(t *testing.T) {
	if os.Getenv("SLUICE_DOCKER_TEST") == "" {
		t.Skip("set SLUICE_DOCKER_TEST=1 to run the Docker sandbox test")
	}
	d := &Docker{images: map[string]string{"python": "python:3.13-slim", "go": "golang:1.26-alpine"}, timeout: 60 * time.Second}
	ctx := context.Background()
	cases := []struct {
		name, lang, sol, test string
		compiled, passed      bool
	}{
		{"py pass", "python", "def add(a, b):\n    return a + b\n", "from solution import add\nassert add(2, 3) == 5\n", true, true},
		{"py fail", "python", "def add(a, b):\n    return a - b\n", "from solution import add\nassert add(2, 3) == 5\n", true, false},
		{"py syntax", "python", "def add(a, b)\n    return a + b\n", "from solution import add\n", false, false},
		{"py no network", "python", "import urllib.request\ndef f():\n    return urllib.request.urlopen('https://example.com', timeout=3).status\n",
			"from solution import f\ntry:\n    f()\n    raise SystemExit(0)\nexcept Exception:\n    raise SystemExit(1)\n", true, false},
		{"go pass", "go", "package main\n\nfunc Rev(s string) string { r := []rune(s); for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 { r[i], r[j] = r[j], r[i] }; return string(r) }\n",
			"package solution\n\nimport \"testing\"\n\nfunc TestRev(t *testing.T) { if Rev(\"héllo\") != \"olléh\" { t.Fatal(Rev(\"héllo\")) } }\n", true, true},
		{"go compile only", "go", "package x\nfunc F() int { return \"s\" }\n", "", false, false},
	}
	for _, c := range cases {
		r, err := d.Run(ctx, c.lang, c.sol, c.test)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if r.Compiled != c.compiled || r.Passed != c.passed {
			t.Errorf("%s: compiled=%v passed=%v (want %v %v); output: %s", c.name, r.Compiled, r.Passed, c.compiled, c.passed, r.Output)
		}
	}
	// Timeout.
	d.timeout = 5 * time.Second
	r, err := d.Run(ctx, "python", "import time\ntime.sleep(60)\n", "import solution\n")
	if err != nil || !r.TimedOut {
		t.Errorf("timeout: %+v %v", r, err)
	}
}
