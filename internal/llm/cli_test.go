package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elcruzo/autoskills/internal/outbound"
)

const testOutputSchema = `{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`

type helperInvocation struct {
	Args             []string `json:"args"`
	Dir              string   `json:"dir"`
	Stdin            string   `json:"stdin"`
	CodexHome        string   `json:"codex_home,omitempty"`
	CodexAuthTarget  string   `json:"codex_auth_target,omitempty"`
	CodexAuthRegular bool     `json:"codex_auth_regular,omitempty"`
	CodexHomeMode    uint32   `json:"codex_home_mode,omitempty"`
	SystemPrompt     string   `json:"system_prompt,omitempty"`
	EnvironmentLeaks []string `json:"environment_leaks,omitempty"`
}

func TestCLIHelper(t *testing.T) {
	if os.Getenv("AUTOSKILLS_CLI_HELPER") == "" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		os.Exit(90)
	}
	args := os.Args[separator+1:]
	stdin, _ := io.ReadAll(os.Stdin)
	dir, _ := os.Getwd()
	codexHome := os.Getenv("CODEX_HOME")
	var codexAuthTarget string
	var codexAuthRegular bool
	var codexHomeMode uint32
	if codexHome != "" {
		codexAuthTarget, _ = os.Readlink(filepath.Join(codexHome, "auth.json"))
		if info, err := os.Lstat(filepath.Join(codexHome, "auth.json")); err == nil {
			codexAuthRegular = info.Mode().IsRegular()
		}
		if info, err := os.Stat(codexHome); err == nil {
			codexHomeMode = uint32(info.Mode().Perm())
		}
	}
	var systemPrompt string
	if systemPromptPath := argumentValue(args, "--system-prompt-file"); systemPromptPath != "" {
		raw, _ := os.ReadFile(systemPromptPath)
		systemPrompt = string(raw)
	}
	var environmentLeaks []string
	for _, name := range []string{"OPENAI_API_KEY", "OpenAI_API_KEY", "CODEX_ACCESS_TOKEN", "Codex_Home", "ANTHROPIC_API_KEY", "Anthropic_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "AWS_SECRET_ACCESS_KEY", "RUST_LOG", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		if _, present := os.LookupEnv(name); present {
			environmentLeaks = append(environmentLeaks, name)
		}
	}
	invocation, _ := json.Marshal(helperInvocation{Args: args, Dir: dir, Stdin: string(stdin), CodexHome: codexHome, CodexAuthTarget: codexAuthTarget, CodexAuthRegular: codexAuthRegular, CodexHomeMode: codexHomeMode, SystemPrompt: systemPrompt, EnvironmentLeaks: environmentLeaks})
	switch os.Getenv("AUTOSKILLS_CLI_HELPER") {
	case "exit":
		_, _ = fmt.Fprint(os.Stderr, "authentication required")
		os.Exit(7)
	case "not-logged-in":
		_, _ = fmt.Fprint(os.Stderr, "Not logged in; run /login")
		os.Exit(7)
	case "authentication-required":
		_, _ = fmt.Fprint(os.Stderr, "authentication required")
		os.Exit(7)
	case "claude-auth-json":
		_, _ = fmt.Fprint(os.Stdout, `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"Not logged in · Please run /login"}`)
		os.Exit(7)
	case "claude-prompt-auth-json":
		_, _ = fmt.Fprint(os.Stdout, `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"authentication required in private prompt"}`)
		os.Exit(8)
	case "request-error":
		_, _ = fmt.Fprint(os.Stderr, "SYSTEM INSTRUCTIONS:\nsystem instruction\nUSER MESSAGE:\nauthentication required in private prompt\nERROR: {\n  \"error\": {\n    \"message\": \"schema rejected\"\n  }\n}\n")
		os.Exit(8)
	case "prompt-error":
		_, _ = fmt.Fprint(os.Stderr, "Error: private-customer-project-codename")
		os.Exit(8)
	case "prompt-json-error":
		_, _ = fmt.Fprint(os.Stderr, `"message": "private-customer-project-codename"`)
		os.Exit(8)
	case "short-prompt-error":
		_, _ = fmt.Fprint(os.Stderr, "Error: x7")
		os.Exit(8)
	case "large":
		large := strings.Repeat("x", maxCLIOutputBytes+1)
		if output := argumentValue(args, "--output-last-message"); output != "" {
			_ = os.WriteFile(output, []byte(`{"value":"`+large+`"}`), 0o600)
			return
		}
		_, _ = fmt.Fprint(os.Stdout, large)
		os.Exit(0)
	case "empty":
		if output := argumentValue(args, "--output-last-message"); output != "" {
			_ = os.WriteFile(output, nil, 0o600)
		}
		_, _ = fmt.Fprint(os.Stdout, `{"type":"result","subtype":"success","is_error":false,"result":""}`)
		os.Exit(0)
	case "invalid":
		if output := argumentValue(args, "--output-last-message"); output != "" {
			_ = os.WriteFile(output, []byte("not json"), 0o600)
		}
		_, _ = fmt.Fprint(os.Stdout, `{"type":"result","subtype":"success","is_error":false,"result":"not json"}`)
		os.Exit(0)
	case "symlink-output":
		if output := argumentValue(args, "--output-last-message"); output != "" {
			target := filepath.Join(filepath.Dir(output), "redirected-output.json")
			_ = os.WriteFile(target, []byte(`{"ok":true}`), 0o600)
			_ = os.Symlink(target, output)
		}
		os.Exit(0)
	case "sleep":
		time.Sleep(5 * time.Second)
	case "spawn-child":
		child := exec.Command(os.Args[0], "-test.run=TestCLIHelper", "--")
		child.Env = append(os.Environ(), "AUTOSKILLS_CLI_HELPER=child-write")
		child.Stdout = io.Discard
		child.Stderr = io.Discard
		if child.Start() != nil {
			os.Exit(91)
		}
		time.Sleep(5 * time.Second)
	case "child-write":
		time.Sleep(300 * time.Millisecond)
		_ = os.WriteFile(os.Getenv("AUTOSKILLS_CHILD_MARKER"), []byte("survived"), 0o600)
		os.Exit(0)
	default:
		if output := argumentValue(args, "--output-last-message"); output != "" {
			_ = os.WriteFile(output, invocation, 0o600)
			return
		}
		envelope, _ := json.Marshal(map[string]any{
			"type": "result", "subtype": "success", "is_error": false, "result": string(invocation),
		})
		_, _ = os.Stdout.Write(envelope)
		os.Exit(0)
	}
}

func argumentValue(args []string, name string) string {
	for i := range args {
		if args[i] == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func helperCommand() []string {
	return []string{os.Args[0], "-test.run=TestCLIHelper", "--"}
}

func newTestCodexProvider(t *testing.T, model string, timeout time.Duration) Provider {
	t.Helper()
	codexHome := t.TempDir()
	authPath := filepath.Join(codexHome, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"tokens":"test-only"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	return newCodexProvider(helperCommand(), model, timeout)
}

func preparedPayload(t *testing.T) outbound.Payload {
	t.Helper()
	var b outbound.Builder
	b.Static("USER DATA: ").Data("token sk-ant-api03-eeeeeeeeeeeeeeeeeeeeeeee", 0)
	p, err := b.BuildWithOutputSchema("system instruction", testOutputSchema)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func decodeInvocation(t *testing.T, output string) helperInvocation {
	t.Helper()
	var invocation helperInvocation
	if err := json.Unmarshal([]byte(output), &invocation); err != nil {
		t.Fatalf("decode invocation: %v\n%s", err, output)
	}
	return invocation
}

func TestCodexProviderInvocationIsEphemeralAndNeutral(t *testing.T) {
	t.Setenv("AUTOSKILLS_CLI_HELPER", "success")
	t.Setenv("OPENAI_API_KEY", "must-not-reach-codex")
	t.Setenv("OpenAI_API_KEY", "must-not-reach-codex")
	t.Setenv("CODEX_ACCESS_TOKEN", "must-not-reach-codex")
	t.Setenv("Codex_Home", "must-not-reach-codex")
	t.Setenv("RUST_LOG", "trace")
	p := newTestCodexProvider(t, "gpt-test", time.Second)
	payload := preparedPayload(t)
	originalDir, _ := os.Getwd()
	out, err := p.Generate(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeInvocation(t, out)
	rel, _ := filepath.Rel(originalDir, got.Dir)
	if rel == "." || !strings.HasPrefix(rel, "..") || !strings.Contains(filepath.Base(got.Dir), "autoskills-llm-") {
		t.Fatalf("working directory is not neutral: %q", got.Dir)
	}
	wantPrefix := []string{"exec", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--strict-config", "-c", "project_doc_max_bytes=0", "-c", "mcp_servers={}", "--enable", "skip_host_skill_discovery", "--disable", "shell_tool", "--disable", "unified_exec", "--disable", "shell_snapshot", "--disable", "code_mode", "--disable", "code_mode_host", "--disable", "code_mode_only", "--disable", "multi_agent", "--disable", "browser_use", "--disable", "browser_use_external", "--disable", "browser_use_full_cdp_access", "--disable", "in_app_browser", "--disable", "computer_use", "--disable", "apps", "--disable", "plugins", "--disable", "plugin_sharing", "--disable", "remote_plugin", "--disable", "hooks", "--disable", "skill_search", "--disable", "skill_mcp_dependency_install", "--disable", "tool_suggest", "--disable", "tool_call_mcp_elicitation", "--disable", "auth_elicitation", "--disable", "goals", "--disable", "workspace_dependencies", "--disable", "in_app_chat", "--disable", "in_app_local_automation", "--disable", "in_app_updates", "--disable", "image_generation", "--disable", "view_image", "--skip-git-repo-check", "--sandbox", "read-only", "--color", "never", "--model", "gpt-test", "--cd", got.Dir, "--output-schema"}
	if len(got.Args) != len(wantPrefix)+4 || !reflect.DeepEqual(got.Args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("argv length = %d, prefix length = %d, argv = %#v, want = %#v", len(got.Args), len(wantPrefix), got.Args, wantPrefix)
	}
	if got.Args[len(got.Args)-3] != "--output-last-message" || got.Args[len(got.Args)-1] != "-" {
		t.Fatalf("argv tail = %#v", got.Args[len(got.Args)-3:])
	}
	wantStdin := "SYSTEM INSTRUCTIONS:\nsystem instruction\n\nUSER MESSAGE:\n" + payload.User()
	if got.Stdin != wantStdin {
		t.Fatalf("stdin = %q", got.Stdin)
	}
	if strings.Contains(got.Stdin, "sk-ant-api03-") {
		t.Fatal("unredacted input reached Codex")
	}
	if got.CodexHome == "" || pathWithin(got.Dir, got.CodexHome) || pathWithin(got.CodexHome, got.Dir) {
		t.Fatalf("CODEX_HOME %q is not isolated from working directory %q", got.CodexHome, got.Dir)
	}
	if runtime.GOOS != "windows" && got.CodexHomeMode != 0o700 {
		t.Fatalf("CODEX_HOME mode = %#o", got.CodexHomeMode)
	}
	if got.CodexAuthTarget != filepath.Join(os.Getenv("CODEX_HOME"), "auth.json") && !got.CodexAuthRegular {
		t.Fatalf("auth.json target = %q, regular = %v", got.CodexAuthTarget, got.CodexAuthRegular)
	}
	for _, arg := range got.Args {
		if got.CodexAuthTarget != "" && strings.Contains(arg, got.CodexAuthTarget) {
			t.Fatalf("authentication path leaked into argv: %q", arg)
		}
	}
	if _, err := os.Stat(got.CodexHome); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary CODEX_HOME was not removed: %v", err)
	}
	if len(got.EnvironmentLeaks) != 0 {
		t.Fatalf("environment reached Codex: %v", got.EnvironmentLeaks)
	}
}

func TestCodexProviderRequiresAuthJSON(t *testing.T) {
	t.Setenv("AUTOSKILLS_CLI_HELPER", "success")
	t.Setenv("CODEX_HOME", t.TempDir())
	_, err := newCodexProvider(helperCommand(), "", time.Second).Generate(context.Background(), preparedPayload(t))
	if !errors.Is(err, ErrNotAuthenticated) || !strings.Contains(err.Error(), "run codex login") {
		t.Fatalf("error = %v", err)
	}
}

func TestCodexProviderRejectsSymlinkedFinalOutput(t *testing.T) {
	t.Setenv("AUTOSKILLS_CLI_HELPER", "symlink-output")
	_, err := newTestCodexProvider(t, "", time.Second).Generate(context.Background(), preparedPayload(t))
	if !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("error = %v", err)
	}
}

func TestCodexAuthenticationFallsBackToProtectedCopy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("production refuses credential copies on Windows")
	}
	source := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(source, []byte("credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "auth.json")
	err := stageCodexAuthWithLink(source, destination, func(string, string) error {
		return errors.New("links unavailable")
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "credential" {
		t.Fatalf("copied auth = %q", raw)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("copied auth mode = %#o", info.Mode().Perm())
	}
}

func TestCodexAuthenticationRefusesUnprotectedCopy(t *testing.T) {
	source := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(source, []byte("credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "auth.json")
	err := stageCodexAuthWithLink(source, destination, func(string, string) error {
		return errors.New("links unavailable")
	}, false)
	if err == nil || !strings.Contains(err.Error(), "symbolic-link support") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("credential copy exists: %v", statErr)
	}
}

func TestClaudeProviderInvocationIsSafeAndNonPersistent(t *testing.T) {
	t.Setenv("AUTOSKILLS_CLI_HELPER", "success")
	t.Setenv("ANTHROPIC_API_KEY", "must-not-reach-claude")
	t.Setenv("Anthropic_API_KEY", "must-not-reach-claude")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "must-not-reach-claude")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "must-not-reach-claude")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "must-not-reach-claude")
	p := newClaudeProvider(helperCommand(), "sonnet", time.Second)
	payload := preparedPayload(t)
	out, err := p.Generate(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeInvocation(t, out)
	wantPrefix := []string{"-p", "--safe-mode", "--disable-slash-commands", "--no-session-persistence", "--tools", "", "--permission-mode", "dontAsk", "--output-format", "json", "--json-schema", testOutputSchema, "--model", "sonnet", "--system-prompt-file"}
	if len(got.Args) != len(wantPrefix)+1 || !reflect.DeepEqual(got.Args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("argv = %#v\nwant prefix %#v", got.Args, wantPrefix)
	}
	if filepath.Dir(got.Args[len(got.Args)-1]) != got.Dir {
		t.Fatalf("system prompt file = %q", got.Args[len(got.Args)-1])
	}
	for _, arg := range got.Args {
		if strings.Contains(arg, payload.System()) {
			t.Fatalf("system prompt leaked into argv: %q", arg)
		}
	}
	if got.Stdin != payload.User() {
		t.Fatalf("stdin = %q", got.Stdin)
	}
	if got.SystemPrompt != payload.System() {
		t.Fatalf("system prompt = %q", got.SystemPrompt)
	}
	if strings.Contains(got.Stdin, "sk-ant-api03-") {
		t.Fatal("unredacted input reached Claude")
	}
	if !strings.Contains(filepath.Base(got.Dir), "autoskills-llm-") {
		t.Fatalf("working directory is not neutral: %q", got.Dir)
	}
	if len(got.EnvironmentLeaks) != 0 {
		t.Fatalf("environment reached Claude: %v", got.EnvironmentLeaks)
	}
}

func TestCLIProvidersRejectFailures(t *testing.T) {
	providers := map[string]func(time.Duration) Provider{
		"codex":  func(timeout time.Duration) Provider { return newTestCodexProvider(t, "", timeout) },
		"claude": func(timeout time.Duration) Provider { return newClaudeProvider(helperCommand(), "", timeout) },
	}
	for name, makeProvider := range providers {
		for _, tc := range []struct {
			behavior string
			want     error
		}{
			{behavior: "exit", want: ErrNotAuthenticated},
			{behavior: "not-logged-in", want: ErrNotAuthenticated},
			{behavior: "empty", want: ErrEmptyOutput},
			{behavior: "invalid", want: ErrInvalidOutput},
		} {
			t.Run(name+"/"+tc.behavior, func(t *testing.T) {
				t.Setenv("AUTOSKILLS_CLI_HELPER", tc.behavior)
				if _, err := makeProvider(time.Second).Generate(context.Background(), preparedPayload(t)); !errors.Is(err, tc.want) {
					t.Fatalf("%s error = %v, want %v", tc.behavior, err, tc.want)
				}
			})
		}
		t.Run(name+"/caller_timeout", func(t *testing.T) {
			t.Setenv("AUTOSKILLS_CLI_HELPER", "sleep")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if _, err := makeProvider(time.Second).Generate(ctx, preparedPayload(t)); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
				t.Fatalf("timeout error = %v", err)
			}
		})
		t.Run(name+"/provider_timeout", func(t *testing.T) {
			t.Setenv("AUTOSKILLS_CLI_HELPER", "sleep")
			if _, err := makeProvider(20*time.Millisecond).Generate(context.Background(), preparedPayload(t)); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("provider timeout error = %v", err)
			}
		})
		t.Run(name+"/cancel", func(t *testing.T) {
			t.Setenv("AUTOSKILLS_CLI_HELPER", "sleep")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := makeProvider(time.Second).Generate(ctx, preparedPayload(t)); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation error = %v", err)
			}
		})
	}
}

func TestCLIProvidersBoundOutput(t *testing.T) {
	for name, provider := range map[string]Provider{
		"codex":  newTestCodexProvider(t, "", time.Second),
		"claude": newClaudeProvider(helperCommand(), "", time.Second),
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("AUTOSKILLS_CLI_HELPER", "large")
			if _, err := provider.Generate(context.Background(), preparedPayload(t)); !errors.Is(err, ErrOutputTooLarge) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCLIProviderCancellationStopsDescendants(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child-survived")
	t.Setenv("AUTOSKILLS_CLI_HELPER", "spawn-child")
	t.Setenv("AUTOSKILLS_CHILD_MARKER", marker)
	_, err := newClaudeProvider(helperCommand(), "", 50*time.Millisecond).Generate(context.Background(), preparedPayload(t))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descendant survived cancellation: %v", err)
	}
}

func TestCLIProviderRefusesTemporaryDirectoryInsideExcludedRoot(t *testing.T) {
	t.Setenv("AUTOSKILLS_CLI_HELPER", "success")
	excluded := t.TempDir()
	t.Setenv("TMPDIR", excluded)
	var builder outbound.Builder
	builder.Static("user")
	payload, err := builder.BuildWithOutputSchema("system", testOutputSchema, excluded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newTestCodexProvider(t, "", time.Second).Generate(context.Background(), payload); err == nil || !strings.Contains(err.Error(), "neutral working directory") {
		t.Fatalf("error = %v", err)
	}
}

func TestCLIProviderAcceptsMissingExcludedRoot(t *testing.T) {
	t.Setenv("AUTOSKILLS_CLI_HELPER", "success")
	excluded := filepath.Join(t.TempDir(), "deleted-repository")
	var builder outbound.Builder
	builder.Static("user")
	payload, err := builder.BuildWithOutputSchema("system", testOutputSchema, excluded)
	if err != nil {
		t.Fatal(err)
	}
	out, err := newClaudeProvider(helperCommand(), "", time.Second).Generate(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeInvocation(t, out).Dir; pathWithin(excluded, got) {
		t.Fatalf("working directory %q is inside missing excluded root %q", got, excluded)
	}
}

func TestCLIProviderResolvesSymlinkedTemporaryRoot(t *testing.T) {
	t.Setenv("AUTOSKILLS_CLI_HELPER", "success")
	excluded := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(excluded, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", alias)
	var builder outbound.Builder
	builder.Static("user")
	payload, err := builder.BuildWithOutputSchema("system", testOutputSchema, excluded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newClaudeProvider(helperCommand(), "", time.Second).Generate(context.Background(), payload); err == nil || !strings.Contains(err.Error(), "neutral working directory") {
		t.Fatalf("error = %v", err)
	}
}

func TestCLIProviderRefusesInstructionBearingTemporaryAncestor(t *testing.T) {
	t.Setenv("AUTOSKILLS_CLI_HELPER", "success")
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "AGENTS.md"), []byte("instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	temporaryBase := filepath.Join(base, "tmp")
	if err := os.Mkdir(temporaryBase, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temporaryBase)
	if _, err := newTestCodexProvider(t, "", time.Second).Generate(context.Background(), preparedPayload(t)); err == nil || !strings.Contains(err.Error(), "configuration or instructions") {
		t.Fatalf("error = %v", err)
	}
}

func TestCLIProviderRefusesConfigurationBearingTemporaryAncestor(t *testing.T) {
	t.Setenv("AUTOSKILLS_CLI_HELPER", "success")
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	temporaryBase := filepath.Join(base, "tmp")
	if err := os.Mkdir(temporaryBase, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temporaryBase)
	if _, err := newClaudeProvider(helperCommand(), "", time.Second).Generate(context.Background(), preparedPayload(t)); err == nil || !strings.Contains(err.Error(), "configuration or instructions") {
		t.Fatalf("error = %v", err)
	}
}

func TestPathWithinUsesPathComponents(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "tmp", "repo")
	for _, tc := range []struct {
		target string
		want   bool
	}{
		{target: root, want: true},
		{target: filepath.Join(root, "tmp"), want: true},
		{target: filepath.Join(string(filepath.Separator), "tmp", "repo-other"), want: false},
		{target: filepath.Join(string(filepath.Separator), "tmp", "..cache"), want: false},
	} {
		if got := pathWithin(root, tc.target); got != tc.want {
			t.Errorf("pathWithin(%q, %q) = %v, want %v", root, tc.target, got, tc.want)
		}
	}
}

func TestCLIProviderRefusesTemporaryDirectoryInsideCallerTree(t *testing.T) {
	t.Setenv("AUTOSKILLS_CLI_HELPER", "success")
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", dir)
	if _, err := newClaudeProvider(helperCommand(), "", time.Second).Generate(context.Background(), preparedPayload(t)); err == nil || !strings.Contains(err.Error(), "neutral working directory") {
		t.Fatalf("error = %v", err)
	}
}

func TestCLIProviderWorksFromFilesystemRoot(t *testing.T) {
	t.Setenv("AUTOSKILLS_CLI_HELPER", "success")
	t.Chdir(filepath.VolumeName(string(filepath.Separator)) + string(filepath.Separator))
	out, err := newClaudeProvider(helperCommand(), "", time.Second).Generate(context.Background(), preparedPayload(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeInvocation(t, out).Dir; !strings.Contains(filepath.Base(got), "autoskills-llm-") {
		t.Fatalf("working directory is not neutral: %q", got)
	}
}

func TestCLIErrorKeepsCauseWithoutEchoingPrompt(t *testing.T) {
	var builder outbound.Builder
	builder.Data("authentication required in private prompt", 0)
	payload, err := builder.BuildWithOutputSchema("system", testOutputSchema)
	if err != nil {
		t.Fatal(err)
	}
	for name, provider := range map[string]Provider{
		"codex":  newTestCodexProvider(t, "", time.Second),
		"claude": newClaudeProvider(helperCommand(), "", time.Second),
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("AUTOSKILLS_CLI_HELPER", "request-error")
			_, err := provider.Generate(context.Background(), payload)
			if err == nil || !strings.Contains(err.Error(), "schema rejected") {
				t.Fatalf("error = %v", err)
			}
			if errors.Is(err, ErrNotAuthenticated) {
				t.Fatalf("prompt text caused false authentication classification: %v", err)
			}
			if strings.Contains(err.Error(), "SYSTEM INSTRUCTIONS") || strings.Contains(err.Error(), "private prompt") {
				t.Fatalf("error echoed prompt: %v", err)
			}
		})
	}
}

func TestCLIAuthenticationClassificationDoesNotEchoPrompt(t *testing.T) {
	for _, tc := range []struct {
		behavior string
		prompt   string
		wantAuth bool
	}{
		{behavior: "authentication-required", prompt: "authentication guidance", wantAuth: true},
		{behavior: "claude-auth-json", prompt: "private project context", wantAuth: true},
		{behavior: "claude-prompt-auth-json", prompt: "authentication required in private prompt", wantAuth: false},
	} {
		t.Run(tc.behavior, func(t *testing.T) {
			t.Setenv("AUTOSKILLS_CLI_HELPER", tc.behavior)
			var builder outbound.Builder
			builder.Data(tc.prompt, 0)
			payload, err := builder.BuildWithOutputSchema("system", testOutputSchema)
			if err != nil {
				t.Fatal(err)
			}
			_, err = newClaudeProvider(helperCommand(), "", time.Second).Generate(context.Background(), payload)
			if err == nil {
				t.Fatal("expected provider error")
			}
			if errors.Is(err, ErrNotAuthenticated) != tc.wantAuth {
				t.Fatalf("error = %v, want authentication classification %v", err, tc.wantAuth)
			}
			if strings.Contains(err.Error(), tc.prompt) {
				t.Fatalf("error echoed prompt: %v", err)
			}
		})
	}
}

func TestCLIErrorDoesNotEchoPromptFragments(t *testing.T) {
	for _, tc := range []struct {
		behavior string
		prompt   string
	}{
		{behavior: "prompt-error", prompt: "private-customer-project-codename"},
		{behavior: "prompt-json-error", prompt: "private-customer-project-codename"},
		{behavior: "short-prompt-error", prompt: "x7"},
	} {
		t.Run(tc.behavior, func(t *testing.T) {
			t.Setenv("AUTOSKILLS_CLI_HELPER", tc.behavior)
			var builder outbound.Builder
			builder.Data(tc.prompt, 0)
			payload, err := builder.BuildWithOutputSchema("system", testOutputSchema)
			if err != nil {
				t.Fatal(err)
			}
			_, err = newClaudeProvider(helperCommand(), "", time.Second).Generate(context.Background(), payload)
			if err == nil || !strings.Contains(err.Error(), "exited non-zero") {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), tc.prompt) {
				t.Fatalf("error echoed prompt: %v", err)
			}
		})
	}
}

func TestProviderGenerateAcceptsOnlyPreparedPayload(t *testing.T) {
	method, ok := reflect.TypeOf((*Provider)(nil)).Elem().MethodByName("Generate")
	if !ok {
		t.Fatal("Generate missing")
	}
	want := reflect.TypeOf(func(context.Context, outbound.Payload) (string, error) { return "", nil })
	if method.Type != want {
		t.Fatalf("Generate type = %v, want %v", method.Type, want)
	}
}
