// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package label

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Numeric answers, GSM8K-style: take the last number in the text (or the
// number on an "ANSWER:" line when present), strip $, commas and a trailing
// period, and compare numerically.

var (
	reAnswerLine = regexp.MustCompile(`(?im)^\s*(?:final\s+)?answer\s*[:=]\s*(.+)$`)
	reNumber     = regexp.MustCompile(`-?\$?\d[\d,]*(?:\.\d+)?|-?\$?\.\d+`)
)

// FinalNumber extracts the final numeric answer from s.
func FinalNumber(s string) (float64, bool) {
	if m := reAnswerLine.FindAllStringSubmatch(s, -1); len(m) > 0 {
		line := m[len(m)-1][1]
		if strings.Contains(strings.ToLower(line), "none") {
			return 0, false
		}
		if nums := reNumber.FindAllString(line, -1); len(nums) > 0 {
			return parseNum(nums[0])
		}
	}
	nums := reNumber.FindAllString(s, -1)
	if len(nums) == 0 {
		return 0, false
	}
	return parseNum(nums[len(nums)-1])
}

func parseNum(s string) (float64, bool) {
	s = strings.NewReplacer("$", "", ",", "").Replace(s)
	s = strings.TrimSuffix(s, ".")
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

// NumEqual compares two answers with a relative tolerance for float noise.
func NumEqual(a, b float64) bool {
	return math.Abs(a-b) <= 1e-6*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// Format checks for the rung-2 parse verifier. The body checked is the
// first fenced block if any, else the whole output.

// FormatOK reports whether out parses as format (json, yaml, csv, xml,
// markdown-table). Unknown formats return ok=false, known=false.
func FormatOK(format, out string) (ok, known bool) {
	body := out
	if _, code, found := FirstCodeBlock(out); found {
		body = code
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return false, true
	}
	switch format {
	case "json":
		var v any
		return json.Unmarshal([]byte(body), &v) == nil, true
	case "yaml":
		var v any
		if yaml.Unmarshal([]byte(body), &v) != nil {
			return false, true
		}
		// A bare scalar is valid YAML but not a YAML document worth the name.
		switch v.(type) {
		case map[string]any, []any:
			return true, true
		}
		return false, true
	case "csv":
		r := csv.NewReader(strings.NewReader(body))
		recs, err := r.ReadAll()
		return err == nil && len(recs) >= 2 && len(recs[0]) >= 2, true
	case "xml":
		dec := xml.NewDecoder(bytes.NewReader([]byte(body)))
		n := 0
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				return n > 0, true
			}
			if err != nil {
				return false, true
			}
			if _, ok := tok.(xml.StartElement); ok {
				n++
			}
		}
	case "markdown-table":
		rows := 0
		sep := false
		for _, line := range strings.Split(body, "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "|") && strings.Count(l, "|") >= 3 {
				rows++
				if strings.Trim(l, "|-: ") == "" {
					sep = true
				}
			}
		}
		return sep && rows >= 3, true
	}
	return false, false
}
