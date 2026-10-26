// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package harvest

import (
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/pemistahl/lingua-go"
)

// Credential and account-identifier patterns (PROJECT.md §5). A match is a
// hard drop of the whole record, never a redaction.
type secretPattern struct {
	Name string
	Re   *regexp.Regexp
	// Check, if set, must also accept the match (used to reject 40-char
	// strings that are plainly not AWS secret keys, such as git SHAs).
	Check func(string) bool
}

var secretPatterns = []secretPattern{
	{Name: "aws-access-key-id", Re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{Name: "aws-secret-key-shape", Re: regexp.MustCompile(`(?:^|[^A-Za-z0-9/+])([A-Za-z0-9/+]{40})(?:[^A-Za-z0-9/+=]|$)`), Check: secretKeyShape},
	{Name: "bearer-token", Re: regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9\-._~+/]{20,}=*`)},
	{Name: "jwt", Re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	{Name: "private-key", Re: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{Name: "github-token", Re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{40,})\b`)},
	{Name: "anthropic-openai-key", Re: regexp.MustCompile(`\bsk-(?:ant-[A-Za-z0-9_-]{20,}|proj-[A-Za-z0-9_-]{20,}|[A-Za-z0-9]{40,})`)},
	{Name: "slack-token", Re: regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`)},
	{Name: "google-api-key", Re: regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{Name: "s3-bucket-uri", Re: regexp.MustCompile(`(?i)(?:\bs3://|arn:aws[a-z-]*:s3:::)([a-z0-9][a-z0-9.-]{2,62})`), Check: notPlaceholderBucket},
}

// AWS context tokens near which a 12-digit number is taken as an account ID.
var (
	reTwelve     = regexp.MustCompile(`(?:^|\D)(\d{12})(?:\D|$)`)
	reAWSContext = regexp.MustCompile(`(?i)arn:|account|aws|\bsts\b|assume|role/|::`)
)

const accountWindow = 40

// Placeholders that identify nothing: generic bucket names used in docs and
// examples, and AWS's documented example account IDs.
var (
	placeholderBuckets  = map[string]bool{"bucket": true, "my-bucket": true, "mybucket": true, "your-bucket": true, "example-bucket": true, "bucket-name": true, "my-bucket-name": true, "some-bucket": true, "foo": true, "bar": true}
	placeholderAccounts = map[string]bool{"123456789012": true, "111122223333": true, "000000000000": true}
)

func notPlaceholderBucket(name string) bool { return !placeholderBuckets[strings.ToLower(name)] }

func secretKeyShape(s string) bool {
	var up, low, dig, hexOnly = false, false, false, true
	for _, r := range s {
		switch {
		case unicode.IsUpper(r):
			up = true
		case unicode.IsLower(r):
			low = true
		case unicode.IsDigit(r):
			dig = true
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			hexOnly = false
		}
	}
	return up && low && dig && !hexOnly
}

// secretMatch returns the name of the first pattern that matches text, and
// the byte offset of the match, or "" if none does.
func secretMatch(text string) (string, int) {
	for _, p := range secretPatterns {
		for _, loc := range p.Re.FindAllStringSubmatchIndex(text, -1) {
			start, end := loc[0], loc[1]
			if len(loc) >= 4 && loc[2] >= 0 {
				start, end = loc[2], loc[3]
			}
			if p.Check != nil && !p.Check(text[start:end]) {
				continue
			}
			return p.Name, start
		}
	}
	for _, loc := range reTwelve.FindAllStringSubmatchIndex(text, -1) {
		s, e := loc[2], loc[3]
		if placeholderAccounts[text[s:e]] {
			continue
		}
		lo, hi := max(0, s-accountWindow), min(len(text), e+accountWindow)
		if reAWSContext.MatchString(text[lo:s]) || reAWSContext.MatchString(text[e:hi]) {
			return "aws-account-id-in-context", s
		}
	}
	return "", -1
}

// English detection (PROJECT.md §5 "non-English"). lingua-go is used
// because it is accurate on short text, which most human turns are. Code
// blocks, URLs, paths and inline code are removed first; text with fewer
// than three prose words (and under 12 letters) left has nothing to judge and is kept as
// "no-prose" (counted separately in lang_detail).

var (
	reFence  = regexp.MustCompile("(?s)```.*?```")
	reInline = regexp.MustCompile("`[^`\n]*`")
	reURL    = regexp.MustCompile(`https?://\S+`)
	rePath   = regexp.MustCompile(`(?:~|\.{0,2})?/[\w.\-/]+`)
	reWord   = regexp.MustCompile(`\pL{2,}`)
)

var langDetector = sync.OnceValue(func() lingua.LanguageDetector {
	return lingua.NewLanguageDetectorBuilder().
		FromLanguages(lingua.English, lingua.Spanish, lingua.French, lingua.German, lingua.Portuguese, lingua.Italian,
			lingua.Dutch, lingua.Russian, lingua.Ukrainian, lingua.Polish, lingua.Turkish, lingua.Chinese, lingua.Japanese,
			lingua.Korean, lingua.Arabic, lingua.Persian, lingua.Hindi, lingua.Vietnamese, lingua.Indonesian, lingua.Swedish,
			lingua.Danish, lingua.Bokmal, lingua.Czech, lingua.Greek, lingua.Hebrew, lingua.Thai, lingua.Romanian, lingua.Hungarian).
		WithPreloadedLanguageModels().
		Build()
})

// englishFloor is the English confidence below which non-English-labelled
// text is dropped. Chosen on the r1 drop sample: misclassified English
// scored 0.11–0.28, real non-English ≤ 0.007.
const englishFloor = 0.05

// LangResult is the outcome of language detection.
type LangResult struct {
	English bool
	Lang    string // ISO 639-3, or "no-prose"
}

func detectEnglish(text string) LangResult {
	prose := reFence.ReplaceAllString(text, " ")
	prose = reInline.ReplaceAllString(prose, " ")
	prose = reURL.ReplaceAllString(prose, " ")
	prose = rePath.ReplaceAllString(prose, " ")
	letters := 0
	for _, r := range prose {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if len(reWord.FindAllString(prose, -1)) < 3 && letters < 12 {
		return LangResult{English: true, Lang: "no-prose"}
	}
	lang, ok := langDetector().DetectLanguageOf(prose)
	if !ok {
		return LangResult{English: true, Lang: "undetermined"}
	}
	if lang == lingua.English {
		return LangResult{English: true, Lang: "eng"}
	}
	// The top label is unstable on short English text (terse commands come
	// out as Dutch or Italian); real non-English text gives English a
	// confidence near zero. Drop only when English is implausible.
	iso := strings.ToLower(lang.IsoCode639_3().String())
	if langDetector().ComputeLanguageConfidence(prose, lingua.English) >= englishFloor {
		return LangResult{English: true, Lang: "eng-low-" + iso}
	}
	return LangResult{English: false, Lang: iso}
}
