// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package harvest

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/abadojack/whatlanggo"
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
	{Name: "s3-bucket-uri", Re: regexp.MustCompile(`(?i)\bs3://[a-z0-9][a-z0-9.-]{2,62}|arn:aws[a-z-]*:s3:::[a-z0-9][a-z0-9.-]{2,62}`)},
}

// AWS context tokens near which a 12-digit number is taken as an account ID.
var (
	reTwelve     = regexp.MustCompile(`(?:^|\D)(\d{12})(?:\D|$)`)
	reAWSContext = regexp.MustCompile(`(?i)arn:|account|aws|\bsts\b|assume|role/|::`)
)

const accountWindow = 40

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
		lo, hi := max(0, s-accountWindow), min(len(text), e+accountWindow)
		if reAWSContext.MatchString(text[lo:s]) || reAWSContext.MatchString(text[e:hi]) {
			return "aws-account-id-in-context", s
		}
	}
	return "", -1
}

// English detection. whatlanggo is run on the text with code blocks, URLs
// and paths removed; text that is mostly code has too little prose to
// detect and is judged by its share of common English function words.

var (
	reFence = regexp.MustCompile("(?s)```.*?```")
	reURL   = regexp.MustCompile(`https?://\S+`)
	rePath  = regexp.MustCompile(`(?:~|\.{0,2})?/[\w.\-/]+`)
	reWord  = regexp.MustCompile(`[A-Za-z']+`)
)

var stopwords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields("the a an and or but if of to in on at for with from by as is are was were be been it this that these those i you we they he she my your our can could should would will do does did not no yes so what how why when where which who please make use add fix run all any some more just also then than there here into out up about") {
		m[w] = true
	}
	return m
}()

// LangResult is the outcome of language detection.
type LangResult struct {
	English bool
	Lang    string // ISO 639-3 from whatlanggo, or "stopword" for the fallback
}

func detectEnglish(text string) LangResult {
	prose := reFence.ReplaceAllString(text, " ")
	prose = reURL.ReplaceAllString(prose, " ")
	prose = rePath.ReplaceAllString(prose, " ")
	// Non-Latin script dominates: not English regardless of word counts.
	var letters, latin int
	for _, r := range prose {
		if unicode.IsLetter(r) {
			letters++
			if unicode.Is(unicode.Latin, r) {
				latin++
			}
		}
	}
	if letters > 0 && float64(latin)/float64(letters) < 0.5 {
		info := whatlanggo.Detect(prose)
		return LangResult{English: false, Lang: info.Lang.Iso6393()}
	}
	words := reWord.FindAllString(strings.ToLower(prose), -1)
	stop := 0
	for _, w := range words {
		if stopwords[w] {
			stop++
		}
	}
	info := whatlanggo.Detect(prose)
	if info.Lang == whatlanggo.Eng && info.IsReliable() {
		return LangResult{English: true, Lang: "eng"}
	}
	// Short or code-heavy text: whatlanggo is unreliable below a few dozen
	// letters, so fall back to function-word share.
	if len(words) >= 3 && float64(stop)/float64(len(words)) >= 0.2 {
		return LangResult{English: true, Lang: "stopword"}
	}
	if info.Lang == whatlanggo.Eng {
		return LangResult{English: true, Lang: "eng-unreliable"}
	}
	return LangResult{English: false, Lang: info.Lang.Iso6393()}
}
