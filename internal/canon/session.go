// Package canon defines the canonical session schema — the contract every collector adapter
// emits and the only shape the distiller ever consumes (PRD §6.1).
package canon

import (
	"strings"
	"time"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Turn struct {
	Role Role
	Text string
}

type Session struct {
	ID        string
	Tool      string // "cursor" | "claude"
	Project   string // human-readable project name
	RepoRoot  string // absolute path to the project root ("" if unresolved)
	Path      string // source transcript file
	StartedAt time.Time
	Turns     []Turn
}

// UserTurns counts turns authored by the human — the primary signal-density heuristic.
func (s *Session) UserTurns() int {
	n := 0
	for _, t := range s.Turns {
		if t.Role == RoleUser {
			n++
		}
	}
	return n
}

// TextSize returns the total character count across all turns.
func (s *Session) TextSize() int {
	n := 0
	for _, t := range s.Turns {
		n += len(t.Text)
	}
	return n
}

// UserSaid reports whether any user-authored turn contains the phrase (case-insensitive).
// Used for trigger-phrase tuned scanning.
func (s *Session) UserSaid(phrase string) bool {
	p := strings.ToLower(phrase)
	for _, t := range s.Turns {
		if t.Role == RoleUser && strings.Contains(strings.ToLower(t.Text), p) {
			return true
		}
	}
	return false
}
