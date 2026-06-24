package writer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elcruzo/autoskills/internal/store"
)

func suggestion(repo string) store.Suggestion {
	return store.Suggestion{
		ID:        "sg_test01",
		Title:     "Use pnpm, never npm",
		Scope:     "repo",
		Placement: "always_on",
		RepoRoot:  repo,
		Body:      "- always `pnpm install`, the preinstall hook redirects npm",
	}
}

func TestAgentsBlockCreateAndIdempotentUpdate(t *testing.T) {
	repo := t.TempDir()
	g := suggestion(repo)

	path, err := Write(g)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "AGENTS.md" {
		t.Fatalf("expected AGENTS.md, got %s", path)
	}
	first, _ := os.ReadFile(path)
	if !strings.Contains(string(first), "autoskills:begin id=sg_test01") {
		t.Fatalf("missing begin marker:\n%s", first)
	}

	// re-accept with edited body: must replace in place, not append
	g.Body = "- EDITED body"
	if _, err := Write(g); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if strings.Count(string(second), "autoskills:begin id=sg_test01") != 1 {
		t.Fatalf("block duplicated:\n%s", second)
	}
	if !strings.Contains(string(second), "EDITED body") || strings.Contains(string(second), "preinstall hook") {
		t.Fatalf("block not replaced:\n%s", second)
	}
	if strings.Count(string(second), sectionBegin) != 1 || strings.Count(string(second), sectionEnd) != 1 {
		t.Fatalf("managed section duplicated:\n%s", second)
	}
}

func TestAgentsBlocksGroupedBySignal(t *testing.T) {
	repo := t.TempDir()

	conv := suggestion(repo) // convention -> Conventions
	conv.Signal = "convention"
	if _, err := Write(conv); err != nil {
		t.Fatal(err)
	}

	pit := suggestion(repo)
	pit.ID = "sg_test_pit"
	pit.Title = "Neo4j ingest fails without DNS"
	pit.Signal = "failure_fix"
	pit.Body = "- check resolv.conf before blaming the driver"
	if _, err := Write(pit); err != nil {
		t.Fatal(err)
	}

	wf := suggestion(repo)
	wf.ID = "sg_test_wf"
	wf.Title = "Catalog rebuild procedure"
	wf.Signal = "workflow"
	wf.Body = "```bash\npython3 scripts/sync.py\n```"
	if _, err := Write(wf); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	s := string(raw)

	// one section, three groups in fixed order, each block under its group
	if strings.Count(s, sectionBegin) != 1 {
		t.Fatalf("expected one managed section:\n%s", s)
	}
	iConv := strings.Index(s, "### Conventions")
	iWf := strings.Index(s, "### Commands & workflows")
	iPit := strings.Index(s, "### Pitfalls")
	if iConv == -1 || iWf == -1 || iPit == -1 || !(iConv < iWf && iWf < iPit) {
		t.Fatalf("group headings missing or out of order (%d, %d, %d):\n%s", iConv, iWf, iPit, s)
	}
	iPnpm := strings.Index(s, "#### Use pnpm, never npm")
	iProc := strings.Index(s, "#### Catalog rebuild procedure")
	iNeo := strings.Index(s, "#### Neo4j ingest fails without DNS")
	if !(iConv < iPnpm && iPnpm < iWf && iWf < iProc && iProc < iPit && iPit < iNeo) {
		t.Fatalf("blocks not under their groups:\n%s", s)
	}
}

func TestLegacyStandaloneBlockAbsorbedIntoSection(t *testing.T) {
	repo := t.TempDir()
	legacy := "# AGENTS.md\n\nhand-written intro\n\n<!-- autoskills:begin id=sg_old -->\n## Old legacy skill\n\n- old body\n<!-- autoskills:end id=sg_old -->\n"
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(suggestion(repo)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	s := string(raw)
	if strings.Count(s, "autoskills:begin id=sg_old") != 1 || !strings.Contains(s, "#### Old legacy skill") {
		t.Fatalf("legacy block not absorbed/normalized:\n%s", s)
	}
	if !strings.Contains(s, "hand-written intro") {
		t.Fatalf("hand-written content lost:\n%s", s)
	}
	if strings.Index(s, "autoskills:begin id=sg_old") < strings.Index(s, sectionBegin) {
		t.Fatalf("legacy block left outside section:\n%s", s)
	}
}

func TestAgentsBlockPreservesHandwrittenContent(t *testing.T) {
	repo := t.TempDir()
	hand := "# AGENTS.md\n\nHand-written intro that must survive.\n"
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(suggestion(repo)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if !strings.Contains(string(raw), "Hand-written intro that must survive.") {
		t.Fatalf("hand-written content lost:\n%s", raw)
	}
}

func TestClaudeImportAddedOnce(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("# CLAUDE.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(suggestion(repo)); err != nil {
		t.Fatal(err)
	}
	g2 := suggestion(repo)
	g2.ID = "sg_test02"
	if _, err := Write(g2); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, "CLAUDE.md"))
	if strings.Count(string(raw), "@AGENTS.md") != 1 {
		t.Fatalf("expected exactly one @AGENTS.md import:\n%s", raw)
	}
}

func TestPathScopedRuleWritesFrontmatter(t *testing.T) {
	repo := t.TempDir()
	g := suggestion(repo)
	g.Placement = "path_scoped"
	g.Globs = "src/**/*.ts"
	path, err := Write(g)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	s := string(raw)
	if !strings.HasSuffix(path, ".mdc") || !strings.Contains(s, `globs: "src/**/*.ts"`) || !strings.Contains(s, "alwaysApply: false") {
		t.Fatalf("bad mdc output at %s:\n%s", path, s)
	}
	if TargetPreview(g) != ".cursor/rules/autoskills-use-pnpm-never-npm.mdc" {
		t.Fatalf("TargetPreview = %q", TargetPreview(g))
	}
}

func TestMachineScopeGoesToHomeSkills(t *testing.T) {
	g := suggestion("")
	g.Scope = "machine"
	path, err := Write(g)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if !strings.Contains(path, filepath.Join(".autoskills", "skills")) {
		t.Fatalf("machine skill in wrong place: %s", path)
	}
}

func TestSlug(t *testing.T) {
	if got := slug("Use pnpm, never npm — preinstall hook!"); got != "use-pnpm-never-npm-preinstall-hook" {
		t.Errorf("slug = %q", got)
	}
}

func TestBudgetDemotesLowestConfidence(t *testing.T) {
	old := SectionBudgetBytes
	SectionBudgetBytes = 900 // tiny budget forces demotion
	defer func() { SectionBudgetBytes = old }()

	repo := t.TempDir()
	long := strings.Repeat("- a fairly long instruction line for padding purposes\n", 8)

	weak := suggestion(repo)
	weak.ID = "sg_weak"
	weak.Title = "Weak low-confidence skill"
	weak.Confidence = 0.55
	weak.Body = long
	if _, err := Write(weak); err != nil {
		t.Fatal(err)
	}

	strong := suggestion(repo)
	strong.ID = "sg_strong"
	strong.Title = "Strong high-confidence skill"
	strong.Confidence = 0.95
	strong.Body = long
	if _, err := Write(strong); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	s := string(raw)
	if strings.Contains(s, "Weak low-confidence skill") {
		t.Fatalf("weak skill should have been demoted out of AGENTS.md:\n%s", s)
	}
	if !strings.Contains(s, "Strong high-confidence skill") {
		t.Fatalf("strong skill missing:\n%s", s)
	}
	demoted := filepath.Join(repo, ".cursor", "skills", "autoskills-weak-low-confidence-skill", "SKILL.md")
	if raw, err := os.ReadFile(demoted); err != nil || !strings.Contains(string(raw), "reason=section-budget") {
		t.Fatalf("demoted skill file missing or unmarked at %s: %v", demoted, err)
	}
}

func TestPruneViaEmptyBodyAndRemove(t *testing.T) {
	repo := t.TempDir()
	g := suggestion(repo)
	written, err := Write(g)
	if err != nil {
		t.Fatal(err)
	}

	// undo path: Remove prunes the block but keeps the rest of the file
	g.WrittenPath = written
	if err := Remove(g); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if strings.Contains(string(raw), "sg_test01") {
		t.Fatalf("block not pruned:\n%s", raw)
	}

	// gardener path: a suggestion with BlockID + empty body prunes that block on accept
	g2 := suggestion(repo)
	g2.ID = "sg_keep"
	if _, err := Write(g2); err != nil {
		t.Fatal(err)
	}
	prune := store.Suggestion{ID: "sg_gardener1", BlockID: "sg_keep", Scope: "repo", Placement: "always_on", RepoRoot: repo, Body: ""}
	if _, err := Write(prune); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if strings.Contains(string(raw), "sg_keep") {
		t.Fatalf("gardener prune failed:\n%s", raw)
	}
}

func TestGardenerAmendRewritesExistingBlock(t *testing.T) {
	repo := t.TempDir()
	orig := suggestion(repo)
	orig.ID = "sg_orig"
	if _, err := Write(orig); err != nil {
		t.Fatal(err)
	}
	amend := store.Suggestion{
		ID: "sg_gardener2", BlockID: "sg_orig", Title: "amend: Use pnpm everywhere",
		Signal: "convention", Scope: "repo", Placement: "always_on", RepoRoot: repo,
		Confidence: 0.9, Body: "- tighter merged body",
	}
	if _, err := Write(amend); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	s := string(raw)
	if strings.Count(s, "autoskills:begin id=sg_orig") != 1 || strings.Contains(s, "id=sg_gardener2") {
		t.Fatalf("amend must rewrite the original block id, not add a new one:\n%s", s)
	}
	if !strings.Contains(s, "#### Use pnpm everywhere") || strings.Contains(s, "amend:") {
		t.Fatalf("amend title not cleaned:\n%s", s)
	}
}

func TestDistillerAmendResolvesByTitle(t *testing.T) {
	repo := t.TempDir()
	orig := suggestion(repo)
	orig.ID = "sg_orig2"
	if _, err := Write(orig); err != nil {
		t.Fatal(err)
	}
	// scan-produced amendment: "amend: <title>" with NO BlockID must rewrite, not duplicate
	amend := suggestion(repo)
	amend.ID = "sg_new_amend"
	amend.Title = "amend: Use pnpm, never npm"
	amend.Body = "- amended via title match"
	if _, err := Write(amend); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	s := string(raw)
	if strings.Contains(s, "id=sg_new_amend") || strings.Count(s, "autoskills:begin") != 1 {
		t.Fatalf("title-matched amend duplicated the block:\n%s", s)
	}
	if !strings.Contains(s, "amended via title match") {
		t.Fatalf("amend body not applied:\n%s", s)
	}
}

func TestRemoveCleansEmittedScript(t *testing.T) {
	repo := t.TempDir()
	g := suggestion(repo)
	g.Placement = "skill"
	g.Title = "Scripted skill"
	g.Body = "```bash\necho hi\n```"
	written, err := Write(g)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(written)
	g.WrittenPath = written
	if err := Remove(g); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(dir)
		t.Fatalf("skill dir not fully cleaned, leftovers: %v", entries)
	}
}

func TestUnknownGroupSurvivesRebuild(t *testing.T) {
	repo := t.TempDir()
	content := "# AGENTS.md\n\n<!-- autoskills:section:begin -->\n## Agent skills (autoskills)\n\n### Conventions\n\n<!-- autoskills:begin id=sg_hand group=misc conf=0.80 -->\n#### Hand-edited oddball\n\n- body\n<!-- autoskills:end id=sg_hand -->\n<!-- autoskills:section:end -->\n"
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(suggestion(repo)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if !strings.Contains(string(raw), "Hand-edited oddball") {
		t.Fatalf("unknown-group block was silently dropped:\n%s", raw)
	}
}

func TestScriptExtractionForProceduralSkills(t *testing.T) {
	repo := t.TempDir()
	g := suggestion(repo)
	g.Placement = "skill"
	g.Title = "Catalog rebuild"
	g.Body = "Rebuild then verify:\n\n```bash\npython3 scripts/sync.py\npython3 scripts/verify_linkage.py\n```"
	path, err := Write(g)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(filepath.Dir(path), "run.sh")
	raw, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("run.sh not written: %v", err)
	}
	if !strings.Contains(string(raw), "set -euo pipefail") || !strings.Contains(string(raw), "verify_linkage.py") {
		t.Fatalf("script content wrong:\n%s", raw)
	}
	if info, _ := os.Stat(script); info.Mode()&0o111 == 0 {
		t.Fatal("run.sh not executable")
	}
}
