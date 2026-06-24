package redact

import (
	"strings"
	"testing"
)

func TestRedactsKnownTokenShapes(t *testing.T) {
	cases := []string{
		"my key is sk-proj-abc123def456ghi789jkl012mno345",
		"export ANTHROPIC_API_KEY=sk-ant-api03-aaaaaaaaaaaaaaaaaaaaaa",
		"token ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA done",
		"aws AKIAIOSFODNN7EXAMPLE here",
		`{"api_key": "supersecretvalue123"}`,
		"Authorization: Bearer abcdefgh12345678",
	}
	for _, c := range cases {
		got := Text(c)
		if got == c {
			t.Errorf("not redacted: %q", c)
		}
		if !strings.Contains(got, "[REDACTED]") {
			t.Errorf("missing placeholder in %q -> %q", c, got)
		}
	}
}

func TestKeepsNormalText(t *testing.T) {
	cases := []string{
		"run pnpm install then pnpm build",
		"the file src/components/Header.tsx has the bug",
		"git commit -m 'fix: resolve linkage'",
		"a normal sentence with no secrets at all",
	}
	for _, c := range cases {
		if got := Text(c); got != c {
			t.Errorf("over-redacted: %q -> %q", c, got)
		}
	}
}

func TestRedactsPrivateKeyBlock(t *testing.T) {
	in := "before\n-----BEGIN RSA PRIVATE KEY-----\nMIIEow...\n-----END RSA PRIVATE KEY-----\nafter"
	got := Text(in)
	if strings.Contains(got, "MIIEow") {
		t.Errorf("private key not redacted: %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Errorf("surrounding text lost: %q", got)
	}
}
