// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package label

import "testing"

func TestFinalNumber(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"So the total is $1,234.50.", 1234.5, true},
		{"Step 1: 3 apples. Step 2: 4 more.\nANSWER: 7", 7, true},
		{"The answer is -12.", -12, true},
		{"ANSWER: none", 0, false},
		{"no digits here", 0, false},
		{"There are 31,622,400 seconds in a leap year.", 31622400, true},
	}
	for _, c := range cases {
		v, ok := FinalNumber(c.in)
		if ok != c.ok || (ok && !NumEqual(v, c.want)) {
			t.Errorf("FinalNumber(%q) = %v,%v want %v,%v", c.in, v, ok, c.want, c.ok)
		}
	}
}

func TestFormatOK(t *testing.T) {
	cases := []struct {
		format, out string
		ok          bool
	}{
		{"json", "```json\n{\"a\": 1}\n```", true},
		{"json", "{\"a\": 1,", false},
		{"yaml", "name: sluice\nversion: 0.1.0\n", true},
		{"yaml", "just a sentence", false},
		{"csv", "a,b\n1,2\n", true},
		{"xml", "<a><b>x</b></a>", true},
		{"xml", "<a><b>x</a>", false},
		{"markdown-table", "| a | b |\n|---|---|\n| 1 | 2 |\n", true},
	}
	for _, c := range cases {
		if ok, known := FormatOK(c.format, c.out); !known || ok != c.ok {
			t.Errorf("FormatOK(%s, %q) = %v (known %v), want %v", c.format, c.out, ok, known, c.ok)
		}
	}
}

func TestFirstCodeBlock(t *testing.T) {
	lang, code, ok := FirstCodeBlock("Here:\n```py\nprint(1)\n```\nand\n```go\nx\n```")
	if !ok || lang != "python" || code != "print(1)\n" {
		t.Errorf("got %q %q %v", lang, code, ok)
	}
}
