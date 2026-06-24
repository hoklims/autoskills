// Package writer emits accepted suggestions into the context files agents actually read.
// All writes are idempotent: repo-file content lives inside autoskills-managed marker blocks
// keyed by suggestion id, so re-accepting updates in place and never clobbers hand-written text.
package writer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/elcruzo/autoskills/internal/config"
	"github.com/elcruzo/autoskills/internal/store"
)

// TargetPreview returns the repo-relative (or ~-relative) destination Write would choose,
// so pending suggestions can show an honest target path in the review UI.
func TargetPreview(g store.Suggestion) string {
	if g.Scope == "machine" || g.RepoRoot == "" {
		return "~/.autoskills/skills/" + slug(g.Title) + ".md"
	}
	switch g.Placement {
	case "path_scoped":
		return ".cursor/rules/autoskills-" + slug(g.Title) + ".mdc"
	case "skill":
		return ".cursor/skills/autoskills-" + slug(g.Title) + "/SKILL.md"
	default:
		return "AGENTS.md"
	}
}

// Write places an accepted suggestion and returns the path written.
// Sensitivity is deliberately NOT enforced here: the review card surfaces the SENSITIVE badge
// and a human approved the write — the badge is the control (PRD §6.7).
//
// Routing:
//   - scope=machine                  -> ~/.autoskills/skills/<slug>.md
//   - placement=path_scoped (repo)   -> <repo>/.cursor/rules/autoskills-<slug>.mdc (globs from TargetPath hint)
//   - placement=skill (repo)         -> <repo>/.cursor/skills/autoskills-<slug>/SKILL.md
//   - placement=always_on (repo)     -> managed block in <repo>/AGENTS.md (+ CLAUDE.md import line if needed)
func Write(g store.Suggestion) (string, error) {
	if g.Scope == "machine" || g.RepoRoot == "" {
		return writeMachineSkill(g)
	}
	switch g.Placement {
	case "path_scoped":
		return writeCursorRule(g)
	case "skill":
		return writeRepoSkill(g)
	default:
		return writeAgentsBlock(g)
	}
}

func writeMachineSkill(g store.Suggestion) (string, error) {
	dir := filepath.Join(config.Dir(), "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, slug(g.Title)+".md")
	content := fmt.Sprintf("# %s\n\n%s\n", g.Title, g.Body)
	return path, os.WriteFile(path, []byte(content), 0o644)
}

func writeCursorRule(g store.Suggestion) (string, error) {
	dir := filepath.Join(g.RepoRoot, ".cursor", "rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "autoskills-"+slug(g.Title)+".mdc")

	globs := strings.TrimSpace(g.Globs)
	var fm strings.Builder
	fm.WriteString("---\n")
	fm.WriteString("description: " + yamlEscape(g.Title) + "\n")
	if globs != "" {
		// always quoted: bare globs starting with * are YAML aliases and would be invalid
		fm.WriteString("globs: \"" + strings.ReplaceAll(globs, `"`, ``) + "\"\n")
		fm.WriteString("alwaysApply: false\n")
	} else {
		fm.WriteString("alwaysApply: true\n")
	}
	fm.WriteString("---\n\n")
	return path, os.WriteFile(path, []byte(fm.String()+g.Body+"\n"), 0o644)
}

func writeRepoSkill(g store.Suggestion) (string, error) {
	dir := filepath.Join(g.RepoRoot, ".cursor", "skills", "autoskills-"+slug(g.Title))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "SKILL.md")
	body := g.Body

	// loop-engineering principle: procedural skills should be runnable, not just readable.
	// A skill whose body carries a shell fence also gets an executable run.sh next to it.
	if script := extractShellScript(g.Body); script != "" {
		scriptPath := filepath.Join(dir, "run.sh")
		if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env bash\nset -euo pipefail\n\n"+script+"\n"), 0o755); err != nil {
			return "", err
		}
		body += "\n\nRunnable: `" + filepath.Join(".cursor", "skills", "autoskills-"+slug(g.Title), "run.sh") + "`"
	}

	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", slug(g.Title), yamlEscape(g.Title), body)
	return path, os.WriteFile(path, []byte(content), 0o644)
}

var shellFenceRe = regexp.MustCompile("(?s)```(?:bash|sh|zsh)\\n(.*?)```")

// extractShellScript returns the concatenated shell fences from a skill body, or "" if none.
func extractShellScript(body string) string {
	var parts []string
	for _, m := range shellFenceRe.FindAllStringSubmatch(body, -1) {
		if s := strings.TrimSpace(m[1]); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// AGENTS.md layout: all autoskills content lives inside ONE managed section, grouped by skill
// kind so related skills read together instead of accumulating chronologically at file end.
const (
	sectionBegin = "<!-- autoskills:section:begin -->"
	sectionEnd   = "<!-- autoskills:section:end -->"
)

var (
	blockRe   = regexp.MustCompile(`(?s)<!-- autoskills:begin id=([^ >]+)(?: group=([a-z]+))?(?: conf=([0-9.]+))? -->\n?(.*?)\n?<!-- autoskills:end id=[^ >]+ -->\n?`)
	sectionRe = regexp.MustCompile(`(?s)\n?` + regexp.QuoteMeta(sectionBegin) + `.*?` + regexp.QuoteMeta(sectionEnd) + `\n?`)
)

// SectionBudgetBytes caps the managed AGENTS.md section. Context is a finite resource — an
// unbounded section degrades the agent it's meant to help ("Markdown poisoning"). On overflow
// the lowest-confidence skills are demoted to on-demand skill files. Callers set this from
// config; the default matches config.SectionBudgetBytes.
var SectionBudgetBytes = 12000

// Block is one managed skill block parsed from an AGENTS.md section.
type Block struct {
	ID         string
	Group      string
	Confidence float64
	Body       string // includes the "#### Title" heading line
}

// ParseBlocks extracts every autoskills-managed block from AGENTS.md content (used by the
// gardener and `verify`).
func ParseBlocks(content string) []Block {
	var out []Block
	for _, m := range blockRe.FindAllStringSubmatch(content, -1) {
		b := Block{ID: m[1], Group: m[2], Body: strings.TrimSpace(m[4])}
		if !knownGroup(b.Group) {
			b.Group = "conventions" // unknown/missing groups must never be silently dropped on rebuild
		}
		if c, err := strconv.ParseFloat(m[3], 64); err == nil {
			b.Confidence = c
		}
		out = append(out, b)
	}
	return out
}

// BlockTitle extracts the human title from a block body's heading line.
func BlockTitle(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if t, ok := strings.CutPrefix(line, "#### "); ok {
			return strings.TrimSpace(t)
		}
		if t, ok := strings.CutPrefix(line, "## "); ok {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

// groups render in this fixed order; a suggestion's signal decides its group.
var groupOrder = []struct{ key, heading string }{
	{"conventions", "Conventions"},
	{"workflows", "Commands & workflows"},
	{"pitfalls", "Pitfalls"},
}

func knownGroup(g string) bool {
	for _, grp := range groupOrder {
		if grp.key == g {
			return true
		}
	}
	return false
}

func groupForSignal(signal string) string {
	switch signal {
	case "rediscovery", "workflow":
		return "workflows"
	case "failure_fix":
		return "pitfalls"
	default: // convention, correction
		return "conventions"
	}
}

func writeAgentsBlock(g store.Suggestion) (string, error) {
	path := filepath.Join(g.RepoRoot, "AGENTS.md")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	content := string(existing)

	// collect every existing managed block (inside the section or legacy standalone ones)
	blocks := map[string]Block{}
	var order []string
	for _, b := range ParseBlocks(content) {
		// legacy blocks used h2 titles; normalize to the grouped layout's h4 (prefix-anchored —
		// a bare Replace would corrupt already-normalized "#### " headings, which contain "## ")
		if strings.HasPrefix(b.Body, "## ") {
			b.Body = "#### " + strings.TrimPrefix(b.Body, "## ")
		}
		blocks[b.ID] = b
		order = append(order, b.ID)
	}

	// upsert (or, for an empty body, prune) the target block
	id := g.BlockID
	if id == "" {
		id = g.ID
		// distiller-proposed amendments ("amend: X") carry no BlockID — resolve the target by
		// title so they rewrite the existing block instead of silently duplicating it
		if t, isAmend := strings.CutPrefix(g.Title, "amend: "); isAmend {
			for _, b := range blocks {
				if strings.EqualFold(BlockTitle(b.Body), strings.TrimSpace(t)) {
					id = b.ID
					break
				}
			}
		}
	}
	if strings.TrimSpace(g.Body) == "" {
		delete(blocks, id) // gardener prune
	} else {
		if _, seen := blocks[id]; !seen {
			order = append(order, id)
		}
		// gardener amendments arrive titled "amend: X" for the inbox; the block heading stays clean
		title := strings.TrimPrefix(g.Title, "amend: ")
		blocks[id] = Block{
			ID:         id,
			Group:      groupForSignal(g.Signal),
			Confidence: g.Confidence,
			Body:       fmt.Sprintf("#### %s\n\n%s", title, strings.TrimSpace(g.Body)),
		}
	}

	// budget enforcement: demote lowest-confidence blocks to on-demand skill files until the
	// section fits. The just-written block is exempt — the human explicitly chose it.
	section := renderSection(blocks, order)
	for len(section) > SectionBudgetBytes && len(blocks) > 1 {
		victim, ok := lowestConfidence(blocks, id)
		if !ok {
			break
		}
		if _, err := demoteToSkillFile(g.RepoRoot, victim); err != nil {
			return "", fmt.Errorf("demote %s: %w", victim.ID, err)
		}
		delete(blocks, victim.ID)
		section = renderSection(blocks, order)
	}

	// strip the old section and any legacy blocks, then rebuild from scratch
	content = sectionRe.ReplaceAllString(content, "\n")
	content = blockRe.ReplaceAllString(content, "")
	content = strings.TrimRight(content, "\n")
	if content == "" {
		content = "# AGENTS.md\n"
	}
	content += "\n" + section

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	ensureClaudeImport(g.RepoRoot)
	return path, nil
}

func renderSection(blocks map[string]Block, order []string) string {
	var sb strings.Builder
	sb.WriteString(sectionBegin + "\n")
	sb.WriteString("## Agent skills (autoskills)\n\n")
	sb.WriteString("Skills distilled from agent sessions and accepted in review. Managed by autoskills — update via `autoskills review`, not by hand.\n")
	for _, grp := range groupOrder {
		var members []Block
		for _, id := range order {
			if b, ok := blocks[id]; ok && b.Group == grp.key {
				members = append(members, b)
			}
		}
		if len(members) == 0 {
			continue
		}
		sb.WriteString("\n### " + grp.heading + "\n")
		for _, b := range members {
			fmt.Fprintf(&sb, "\n<!-- autoskills:begin id=%s group=%s conf=%.2f -->\n%s\n<!-- autoskills:end id=%s -->\n", b.ID, b.Group, b.Confidence, b.Body, b.ID)
		}
	}
	sb.WriteString(sectionEnd + "\n")
	return sb.String()
}

func lowestConfidence(blocks map[string]Block, exemptID string) (Block, bool) {
	var victim Block
	found := false
	for _, b := range blocks {
		if b.ID == exemptID {
			continue
		}
		if !found || b.Confidence < victim.Confidence {
			victim, found = b, true
		}
	}
	return victim, found
}

// demoteToSkillFile moves an over-budget block to an on-demand skill file so it stays
// available without occupying always-on context.
func demoteToSkillFile(repoRoot string, b Block) (string, error) {
	title := BlockTitle(b.Body)
	if title == "" {
		title = b.ID
	}
	dir := filepath.Join(repoRoot, ".cursor", "skills", "autoskills-"+slug(title))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "SKILL.md")
	body := strings.TrimSpace(strings.TrimPrefix(b.Body, "#### "+title))
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n\n<!-- autoskills:demoted id=%s reason=section-budget -->\n", slug(title), yamlEscape(title), body, b.ID)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "  budget: demoted %q to %s\n", title, path)
	return path, nil
}

// Remove undoes an accepted suggestion's artifact. AGENTS.md blocks are pruned from the
// managed section; standalone files are deleted.
func Remove(g store.Suggestion) error {
	if g.WrittenPath == "" {
		return nil
	}
	if filepath.Base(g.WrittenPath) == "AGENTS.md" {
		gg := g
		gg.Body = "" // empty body = prune
		_, err := writeAgentsBlock(gg)
		return err
	}
	if err := os.Remove(g.WrittenPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	dir := filepath.Dir(g.WrittenPath)
	// skill dirs may also carry an emitted run.sh — remove it before the empty-dir tidy,
	// but only inside directories we own (autoskills- prefix), never a user's dir
	if strings.HasPrefix(filepath.Base(dir), "autoskills-") {
		_ = os.Remove(filepath.Join(dir, "run.sh"))
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
	return nil
}

// ensureClaudeImport makes an existing CLAUDE.md pick up AGENTS.md content via the official
// @import syntax. Only touches CLAUDE.md if it already exists and lacks any AGENTS.md reference.
func ensureClaudeImport(repoRoot string) {
	path := filepath.Join(repoRoot, "CLAUDE.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if strings.Contains(string(raw), "AGENTS.md") {
		return
	}
	content := string(raw)
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n@AGENTS.md\n"
	_ = os.WriteFile(path, []byte(content), 0o644)
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.ToLower(s)
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 48 {
		s = s[:48]
		s = strings.Trim(s, "-")
	}
	if s == "" {
		s = "skill"
	}
	return s
}

func yamlEscape(s string) string {
	if strings.ContainsAny(s, ":#{}[],&*?|->!%@`\"'") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}
