// Package redact strips credential-shaped strings from transcript text BEFORE it reaches any
// LLM endpoint — even a local one (PRD §10.1). Pattern-based plus an entropy heuristic for
// long opaque tokens assigned to secret-looking variable names.
package redact

import (
	"math"
	"regexp"
	"strings"
)

const placeholder = "[REDACTED]"

var patterns = []*regexp.Regexp{
	// provider/service tokens with well-known prefixes
	regexp.MustCompile(`\b(sk|pk|rk)-[A-Za-z0-9_-]{16,}\b`),                 // OpenAI/Stripe-style
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{16,}\b`),                     // Anthropic
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),                    // GitHub tokens
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`),                  // GitHub fine-grained
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),                  // Slack
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),                              // AWS access key id
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}\b`),                        // Google API key
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`), // JWT
	// private key blocks
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	// assignments to secret-looking names: api_key=..., "TOKEN": "...", password = '...'
	regexp.MustCompile(`(?i)\b([a-z0-9_]*(?:api[_-]?key|secret|token|passwd|password|credential)[a-z0-9_]*)['"]?\s*[:=]\s*['"]?[^\s'"]{8,}['"]?`),
	// bearer headers
	regexp.MustCompile(`(?i)\bauthorization\s*[:=]\s*['"]?bearer\s+[^\s'"]{8,}['"]?`),
}

// assignment-style patterns keep the variable name and redact only the value
var keepNamePattern = regexp.MustCompile(`(?i)^([a-z0-9_]*(?:api[_-]?key|secret|token|passwd|password|credential)[a-z0-9_]*['"]?\s*[:=]\s*)`)

// Text replaces credential-shaped substrings with [REDACTED].
func Text(s string) string {
	for _, re := range patterns {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			if name := keepNamePattern.FindString(m); name != "" {
				return name + placeholder
			}
			return placeholder
		})
	}
	return redactHighEntropyTokens(s)
}

// redactHighEntropyTokens catches opaque 32+ char tokens that escaped the named patterns.
// Conservative on purpose: long hex/base64 runs with high Shannon entropy only — file hashes
// in normal dev output do get caught, which is an acceptable loss for transcripts.
func redactHighEntropyTokens(s string) string {
	tokenRe := regexp.MustCompile(`\b[A-Za-z0-9+/_=-]{40,}\b`)
	return tokenRe.ReplaceAllStringFunc(s, func(m string) string {
		if strings.Contains(m, "/") && strings.Count(m, "/") > 2 {
			return m // path-like, keep
		}
		if shannonEntropy(m) > 4.2 {
			return placeholder
		}
		return m
	})
}

func shannonEntropy(s string) float64 {
	if s == "" {
		return 0
	}
	freq := map[rune]float64{}
	for _, r := range s {
		freq[r]++
	}
	var e float64
	n := float64(len(s))
	for _, c := range freq {
		p := c / n
		e -= p * math.Log2(p)
	}
	return e
}
