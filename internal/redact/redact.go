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

// secretName is the alternation of variable-name fragments that mark a value as credential-shaped.
// Kept in one place so the redacting pattern and the keep-the-name pattern can never drift.
const secretName = `(?:api[_-]?key|account[_-]?key|access[_-]?key|private[_-]?key|secret|token|passwd|password|credential)`

var patterns = []*regexp.Regexp{
	// provider/service tokens with well-known prefixes
	regexp.MustCompile(`\b(sk|pk|rk)-[A-Za-z0-9_-]{16,}\b`),                                 // OpenAI/Stripe-style
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{16,}\b`),                                     // Anthropic
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),                                    // GitHub tokens
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`),                                  // GitHub fine-grained
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),                                  // Slack
	regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),                                     // AWS access key id (incl. temporary)
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}\b`),                                        // Google API key
	regexp.MustCompile(`\bya29\.[A-Za-z0-9_-]{20,}`),                                        // Google OAuth access token
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`), // JWT
	regexp.MustCompile(`https://hooks\.slack\.com/services/[A-Za-z0-9/_-]+`),                // webhook URL = credential
	regexp.MustCompile(`\b[A-Za-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+\b`), // email / identity data
	// private key blocks
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	// URLs carrying credentials in userinfo — DSNs and connection strings live here:
	// postgres://user:pass@db/app, mongodb+srv://…, amqp://…, https://user:token@host
	regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s/:@]+:[^\s/@]+@\S+`),
	// assignments to secret-looking names: api_key=..., "TOKEN": "...", password = '...',
	// AccountKey=... in an Azure connection string, .env lines
	regexp.MustCompile(`(?i)\b([a-z0-9_]*` + secretName + `[a-z0-9_]*)['"]?\s*[:=]\s*['"]?[^\s'"]{8,}['"]?`),
	// bearer credentials, with or without the Authorization header around them
	regexp.MustCompile(`(?i)\bauthorization\s*[:=]\s*['"]?bearer\s+[^\s'"]{8,}['"]?`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{16,}`),
	// internal destinations: RFC1918 hosts, and any host carrying an internal/intranet/corp/lan
	// label. The label may sit anywhere in the host (api.corp.example.com counts) — deliberate:
	// over-redacting a corporate hostname is cheaper than leaking one. Loopback is deliberately
	// kept: http://localhost:3000 is dev noise, not an asset, and redacting it would gut evidence.
	regexp.MustCompile(`(?i)\bhttps?://(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}|(?:[a-z0-9-]+\.)*(?:internal|intranet|corp|lan)(?:\.[a-z0-9-]+)*)(?::\d+)?\S*`),
}

// assignment-style patterns keep the variable name and redact only the value
var keepNamePattern = regexp.MustCompile(`(?i)^([a-z0-9_]*` + secretName + `[a-z0-9_]*['"]?\s*[:=]\s*)`)

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
