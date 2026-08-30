package distill

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// A provider response is untrusted input, exactly like a transcript. These bounds are the closed
// schema it must fit: enums are checked, never coerced, and a value outside them drops the
// suggestion instead of being repaired into something writable.
const (
	maxMetadataBytes        = 512
	maxExistingContextBytes = 14000

	maxTitleBytes     = 200
	maxBodyBytes      = 8000
	maxRationaleBytes = 1000

	minExcerptBytes          = 10
	maxExcerptBytes          = 300
	maxEvidencePerSuggestion = 3
	maxEvidenceOffered       = 16

	maxGlobs     = 10
	maxGlobBytes = 200
)

var (
	validSignals = []string{"correction", "rediscovery", "failure_fix", "convention", "workflow"}
	validActions = []string{"amend", "prune"}
)

// validate applies the closed schema to one proposed suggestion.
func (r rawSuggestion) validate() error {
	title := strings.TrimSpace(r.Title)
	switch {
	case title == "":
		return errors.New("empty title")
	case len(title) > maxTitleBytes:
		return fmt.Errorf("title of %d bytes exceeds %d", len(title), maxTitleBytes)
	}
	body := strings.TrimSpace(r.Body)
	switch {
	case body == "":
		return errors.New("empty body")
	case len(body) > maxBodyBytes:
		return fmt.Errorf("body of %d bytes exceeds %d", len(body), maxBodyBytes)
	}
	if strings.TrimSpace(r.Rationale) == "" {
		return errors.New("empty rationale")
	}
	if n := len(strings.TrimSpace(r.Rationale)); n > maxRationaleBytes {
		return fmt.Errorf("rationale of %d bytes exceeds %d", n, maxRationaleBytes)
	}
	if r.Confidence == nil || r.Sensitivity == nil {
		return errors.New("missing confidence or sensitivity")
	}
	if math.IsNaN(*r.Confidence) || *r.Confidence < 0 || *r.Confidence > 1 {
		return fmt.Errorf("confidence %v outside [0,1]", *r.Confidence)
	}
	if err := checkEnum("signal", r.Signal, validSignals); err != nil {
		return err
	}
	if len(r.Globs) > maxGlobs {
		return fmt.Errorf("%d globs exceed %d", len(r.Globs), maxGlobs)
	}
	for _, g := range r.Globs {
		if len(g) > maxGlobBytes {
			return fmt.Errorf("glob of %d bytes exceeds %d", len(g), maxGlobBytes)
		}
		// globs are stored comma-joined and rendered into YAML frontmatter
		if strings.ContainsAny(g, ",\n\r\"") {
			return fmt.Errorf("glob %q contains a separator or quote", truncateForLog(g))
		}
	}
	if len(r.Evidence) > maxEvidenceOffered {
		return fmt.Errorf("%d evidence excerpts exceed %d", len(r.Evidence), maxEvidenceOffered)
	}
	if len(r.Evidence) == 0 {
		return errors.New("missing evidence")
	}
	return nil
}

// validate applies the closed schema to one proposed gardening action. A prune carries no body;
// an amend must carry both title and body.
func (a gardenAction) validate() error {
	if err := checkEnum("type", a.Type, validActions); err != nil {
		return err
	}
	if strings.TrimSpace(a.BlockID) == "" || strings.TrimSpace(a.Rationale) == "" {
		return errors.New("garden action needs block_id and rationale")
	}
	if a.Confidence == nil {
		return errors.New("garden action needs confidence")
	}
	if math.IsNaN(*a.Confidence) || *a.Confidence < 0 || *a.Confidence > 1 {
		return fmt.Errorf("confidence %v outside [0,1]", *a.Confidence)
	}
	if n := len(strings.TrimSpace(a.Rationale)); n > maxRationaleBytes {
		return fmt.Errorf("rationale of %d bytes exceeds %d", n, maxRationaleBytes)
	}
	if strings.ToLower(strings.TrimSpace(a.Type)) == "amend" {
		title := strings.TrimSpace(a.Title)
		body := strings.TrimSpace(a.Body)
		switch {
		case title == "" || body == "":
			return errors.New("amend needs both title and body")
		case len(title) > maxTitleBytes:
			return fmt.Errorf("title of %d bytes exceeds %d", len(title), maxTitleBytes)
		case len(body) > maxBodyBytes:
			return fmt.Errorf("body of %d bytes exceeds %d", len(body), maxBodyBytes)
		}
	}
	return nil
}

func checkEnum(field, value string, allowed []string) error {
	v := strings.ToLower(strings.TrimSpace(value))
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return fmt.Errorf("%s %q is not one of %s", field, truncateForLog(value), strings.Join(allowed, "|"))
}

func truncateForLog(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
