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

	"github.com/elcruzo/autoskills/internal/store"
)

// Write places an accepted suggestion and returns the path written.
// Sensitivity is deliberately NOT enforced here: the review card surfaces the SENSITIVE badge
// and a human approved the write — the badge is the control (PRD §6.7).
//
// The destination is never read off the suggestion: BuildPlan recomputes it locally and refuses
// anything outside the closed set of artifacts, so an invalid or out-of-tree plan is an error
// rather than a file.
//
// Routing:
//   - scope=machine                  -> ~/.autoskills/skills/<slug>.md
//   - placement=path_scoped (repo)   -> <repo>/.cursor/rules/autoskills-<slug>.mdc
//   - placement=skill (repo)         -> <repo>/.cursor/skills/autoskills-<slug>/SKILL.md
//   - placement=always_on (repo)     -> managed block in <repo>/AGENTS.md (+ CLAUDE.md import line if needed)
func Write(g store.Suggestion) (string, error) {
	plan, err := BuildPlan(g)
	if err != nil {
		return "", err
	}
	switch plan.Kind {
	case KindMachineSkill:
		return writeMachineSkill(g, plan)
	case KindCursorRule:
		return writeCursorRule(g, plan)
	case KindRepoSkill:
		return writeRepoSkill(g, plan)
	default:
		return writeAgentsBlock(g, plan)
	}
}

func writeMachineSkill(g store.Suggestion, plan Plan) (string, error) {
	if err := os.MkdirAll(filepath.Dir(plan.Path), 0o755); err != nil {
		return "", err
	}
	content := fmt.Sprintf("# %s\n\n%s\n", g.Title, g.Body)
	return plan.Path, os.WriteFile(plan.Path, []byte(content), 0o644)
}

func writeCursorRule(g store.Suggestion, plan Plan) (string, error) {
	if err := os.MkdirAll(filepath.Dir(plan.Path), 0o755); err != nil {
		return "", err
	}
	path := plan.Path

	globs := strings.TrimSpace(g.Globs)
	if globs == "" {
		return "", fmt.Errorf("writer: refusing a path_scoped rule without globs")
	}
	var fm strings.Builder
	fm.WriteString("---\n")
	fm.WriteString("description: " + yamlEscape(g.Title) + "\n")
	// always quoted: bare globs starting with * are YAML aliases and would be invalid
	fm.WriteString("globs: \"" + strings.ReplaceAll(globs, `"`, ``) + "\"\n")
	fm.WriteString("alwaysApply: false\n")
	fm.WriteString("---\n\n")
	return path, os.WriteFile(path, []byte(fm.String()+g.Body+"\n"), 0o644)
}

// writeRepoSkill emits an on-demand skill file. Shell fences in the body stay what they are —
// inert Markdown a human reads and runs deliberately. AutoSkills used to extract them into an
// executable run.sh (0755) next to the skill; that turned model-authored text into a program on
// disk and was removed in HOK-539. No artifact this package writes is executable.
func writeRepoSkill(g store.Suggestion, plan Plan) (string, error) {
	if err := os.MkdirAll(filepath.Dir(plan.Path), 0o755); err != nil {
		return "", err
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", slug(g.Title), yamlEscape(g.Title), g.Body)
	return plan.Path, os.WriteFile(plan.Path, []byte(content), 0o644)
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

func writeAgentsBlock(g store.Suggestion, plan Plan) (string, error) {
	path := plan.Path
	// AGENTS.md writes can have two bounded secondary effects: budget demotion under .cursor and
	// an import appended to an existing CLAUDE.md. Reject an unsafe CLAUDE.md link before any
	// mutation; each demotion is checked at its own destination below.
	if err := validateClaudeImport(g.RepoRoot); err != nil {
		return "", err
	}
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
	if err := ensureClaudeImport(g.RepoRoot); err != nil {
		return "", err
	}
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
	path := filepath.Join(dir, "SKILL.md")
	if err := confine(repoRoot, path); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
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
		plan, err := BuildPlan(gg)
		if err != nil {
			return err
		}
		_, err = writeAgentsBlock(gg, plan)
		return err
	}
	if err := os.Remove(g.WrittenPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	dir := filepath.Dir(g.WrittenPath)
	// older versions emitted an executable run.sh next to a skill (removed in HOK-539); undo must
	// still clean one up when it exists, but only inside directories we own (autoskills- prefix)
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
func validateClaudeImport(repoRoot string) error {
	path := filepath.Join(repoRoot, "CLAUDE.md")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return confine(repoRoot, path)
}

func ensureClaudeImport(repoRoot string) error {
	path := filepath.Join(repoRoot, "CLAUDE.md")
	if err := validateClaudeImport(repoRoot); err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if strings.Contains(string(raw), "AGENTS.md") {
		return nil
	}
	content := string(raw)
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n@AGENTS.md\n"
	return os.WriteFile(path, []byte(content), 0o644)
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
