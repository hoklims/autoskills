package distill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elcruzo/autoskills/internal/store"
	"github.com/elcruzo/autoskills/internal/writer"
)

const gardenSystemPrompt = `You are the gardener inside AutoSkills. Your job is the OPPOSITE of adding: you keep an agent-context file healthy by tightening, merging, and removing skills. A bloated or stale skill section makes coding agents WORSE ("Markdown poisoning") — every byte must earn its place.

You receive the managed skill blocks from a repo's AGENTS.md. Propose actions:
- "amend": rewrite a block tighter (merge overlapping blocks into the strongest one by amending it to cover both; shorten verbose bodies; replace prose with commands/tables). Provide the FULL replacement title and body.
- "prune": remove a block that is stale, generic, redundant after a merge, or unlikely to change agent behavior. Body not needed.

Rules:
- Be conservative: only propose actions you are confident improve the file. Zero actions is a fine outcome.
- When merging A into B: one "amend" for B (covering both) plus one "prune" for A.
- Bodies follow the house style: fenced commands, exact paths in backticks, tables, short imperative bullets. 3-15 lines.
- rationale: one sentence; for prunes, say exactly why it no longer earns its place.
- confidence: 0.0-1.0 that the user accepts the action.

Respond with ONLY a JSON object, no fences:
{"actions":[{"type":"amend|prune","block_id":"sg_x","title":"...","body":"...","rationale":"...","confidence":0.0}]}`

type gardenAction struct {
	Type       string  `json:"type"`
	BlockID    string  `json:"block_id"`
	Title      string  `json:"title"`
	Body       string  `json:"body"`
	Rationale  string  `json:"rationale"`
	Confidence float64 `json:"confidence"`
}

type gardenResponse struct {
	Actions []gardenAction `json:"actions"`
}

// signalForGroup maps a block's group back to a signal so amended blocks keep their grouping.
func signalForGroup(group string) string {
	switch group {
	case "workflows":
		return "workflow"
	case "pitfalls":
		return "failure_fix"
	default:
		return "convention"
	}
}

// Garden reviews a repo's managed skill blocks and returns amend/prune suggestions for the
// normal review inbox. Gardener suggestions carry no transcript evidence — their evidence is
// the current state of the file itself, which the review card shows via the block id.
func (d *Distiller) Garden(ctx context.Context, repoRoot, project string) ([]store.Suggestion, error) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "AGENTS.md"))
	if err != nil {
		return nil, fmt.Errorf("garden: no AGENTS.md in %s", repoRoot)
	}
	blocks := writer.ParseBlocks(string(raw))
	if len(blocks) == 0 {
		return nil, nil
	}

	byID := map[string]writer.Block{}
	var sb strings.Builder
	sb.WriteString("REPO: " + repoRoot + "\n\nMANAGED BLOCKS:\n")
	for _, b := range blocks {
		byID[b.ID] = b
		fmt.Fprintf(&sb, "\n[block_id=%s group=%s conf=%.2f]\n%s\n", b.ID, b.Group, b.Confidence, b.Body)
	}
	sb.WriteString("\nTASK: propose amend/prune actions per your system instructions. JSON only.")

	out, err := d.Client.Chat(ctx, gardenSystemPrompt, sb.String())
	if err != nil {
		return nil, err
	}
	var resp gardenResponse
	if err := json.Unmarshal([]byte(extractJSON(out)), &resp); err != nil {
		return nil, fmt.Errorf("garden: unparseable response: %w", err)
	}

	var result []store.Suggestion
	for _, a := range resp.Actions {
		b, known := byID[a.BlockID]
		if !known || a.Confidence < d.MinConfidence {
			continue
		}
		g := store.Suggestion{
			ID:         "sg_" + randomID(),
			CreatedAt:  time.Now(),
			Status:     "pending",
			Signal:     signalForGroup(b.Group),
			Scope:      "repo",
			Placement:  "always_on",
			Confidence: a.Confidence,
			Project:    project,
			RepoRoot:   repoRoot,
			TargetPath: "AGENTS.md",
			Rationale:  strings.TrimSpace(a.Rationale),
			BlockID:    a.BlockID,
			Tool:       "gardener",
		}
		switch a.Type {
		case "amend":
			if a.Title == "" || strings.TrimSpace(a.Body) == "" {
				continue
			}
			g.Title = "amend: " + strings.TrimSpace(a.Title)
			g.Body = strings.TrimSpace(a.Body)
		case "prune":
			title := writer.BlockTitle(b.Body)
			if title == "" {
				title = a.BlockID
			}
			g.Title = "prune: " + title
			g.Body = "" // empty body = the writer removes the block on accept
		default:
			continue
		}
		result = append(result, g)
	}
	return result, nil
}
