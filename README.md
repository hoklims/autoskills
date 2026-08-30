# autoskills

Turn your AI coding sessions into reviewed, committed skills.

AutoSkills reads the transcripts your coding agents already write to disk — Cursor, Claude Code — and distills them into suggested skills: the conventions you keep repeating, the pitfalls your agent keeps rediscovering, the corrections you keep making. You review each suggestion against its verbatim transcript evidence, accept or reject, and AutoSkills writes the accepted ones into the files your agents actually read: `AGENTS.md`, `CLAUDE.md` (via import), `.cursor/rules/*.mdc`, `SKILL.md`.

**Nothing ever leaves your machine.** Transcripts are parsed locally; distillation calls go directly from your machine to the LLM endpoint *you* configure (your API key, your company's gateway, or local Ollama). There is no autoskills server. The review dashboard is served by the binary at `localhost`.

## Install

```bash
go build -o autoskills ./cmd/autoskills   # from source (Go 1.25+)
```

To get the full dashboard UI embedded in the binary, build the frontend first:

```bash
cd web && bun install && bun run build && cd ..
go build -o autoskills ./cmd/autoskills
```

## Use

```bash
# 1. configure your LLM (or use env vars)
export ANTHROPIC_API_KEY=sk-ant-...        # or OPENAI_API_KEY / AUTOSKILLS_API_KEY

# 2. see what's on this machine
autoskills status

# 3. distill recent sessions into suggestions
autoskills scan                            # --project NAME, --since 168h, --max 20, --dry-run

# 4. review: accept / edit / reject — accepted skills are written to your repos
autoskills review                          # opens http://127.0.0.1:4517

# always-on mode: watches transcript dirs and auto-scans seconds after a session settles
autoskills install-daemon                  # or run `autoskills daemon` manually

# maintenance
autoskills garden                          # LLM pass: propose amend/merge/prune → review inbox
autoskills verify                          # flag skills referencing missing or recently-changed paths
autoskills undo sg_xxx                     # revert an accepted skill (removes the artifact)
```

Config lives at `~/.autoskills/config.json`:

```json
{
  "endpoint": "https://api.anthropic.com/v1",
  "api_key": "sk-ant-...",
  "model": "claude-sonnet-4-5",
  "max_suggestions_per_scan": 10,
  "min_confidence": 0.5,
  "ignore_projects": [],
  "trigger_phrase": "",
  "auto_accept_threshold": 0,
  "section_budget_bytes": 12000,
  "daemon_interval_minutes": 30
}
```

The automation dial runs in both directions: set `trigger_phrase` (e.g. `"autoskills this"`) to distill only sessions where you typed that phrase, or set `auto_accept_threshold` (e.g. `0.95`) to let high-confidence non-sensitive skills skip the inbox entirely (every auto-accept is reversible with `autoskills undo`). `section_budget_bytes` caps the managed AGENTS.md section — on overflow, the lowest-confidence skills are demoted to on-demand skill files so always-on context never bloats ("Markdown poisoning" defense).

Any OpenAI-compatible endpoint works: OpenAI (`https://api.openai.com/v1`), a corporate LLM gateway, or local Ollama (`http://localhost:11434/v1`, no key).

## How it decides what to suggest

The distiller hunts for five signal types — user corrections, repeated rediscovery, failure→fix sequences, stated conventions, repeatable workflows — and is heavily quality-gated: most sessions produce zero suggestions, every suggestion must carry verbatim transcript evidence (validated by exact substring match), credential-shaped strings are redacted before any LLM call, and suggestions deduplicate against your existing rules files and prior suggestions. A cheap deterministic pre-filter runs first: a session with none of those markers is skipped before any LLM call, so the model is only spent on transcripts that actually contain signal.

Accepted skills are placed deliberately (always-on rule vs path-scoped rule vs on-demand skill). In `AGENTS.md` they live inside a single managed section, grouped so related skills read together instead of accumulating chronologically:

```markdown
<!-- autoskills:section:begin -->
## Agent skills (autoskills)

### Conventions
<!-- autoskills:begin id=sg_xxx group=conventions -->
#### Use pnpm, never npm — preinstall hook redirects
...
<!-- autoskills:end id=sg_xxx -->

### Commands & workflows
### Pitfalls
<!-- autoskills:section:end -->
```

Re-accepting updates a block in place, groups stay sorted, and your hand-written content outside the section is never touched. Skill bodies are written for machines: fenced commands, exact paths, tables — not prose.

## What leaves your machine

- Redacted transcript excerpts → your configured LLM endpoint (and nowhere else)
- Nothing → us. There is no telemetry, no account, no server in v0.1.

## Platform support

| Platform | Core (scan/review/garden/verify) | Background service |
|---|---|---|
| macOS | supported (primary dev platform) | `install-daemon` → launchd |
| Linux | supported (binary cross-compiles CGO-free; amd64 + arm64) | `install-daemon` → systemd user unit |
| Windows | compiles and should work (`%USERPROFILE%\.cursor`, `%USERPROFILE%\.claude`) — not yet exercised | run `autoskills daemon` manually / Task Scheduler (native service on the roadmap) |

WSL note: transcripts written by Windows-side tools live under `/mnt/c/Users/<you>/...` — point `CURSOR_DIR`-style overrides there (first-class WSL detection is on the roadmap).

## Repo layout

- `cmd/autoskills` — CLI (scan / review / status)
- `internal/` — collectors (Cursor, Claude Code), distiller, writers, local API server
- `web/` — review dashboard (Vite + React + SCSS), embedded into the binary

The marketing site and the Mintlify documentation are maintained in separate repositories.

## License

MIT
