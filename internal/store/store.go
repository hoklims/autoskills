// Package store persists ingest progress and suggestions in a local SQLite database at
// ~/.autoskills/autoskills.db. Everything AutoSkills knows lives here — there is no server.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type Evidence struct {
	Excerpt   string `json:"excerpt"`
	SessionID string `json:"sessionId"`
	Tool      string `json:"tool"`
}

type Suggestion struct {
	ID          string     `json:"id"`
	CreatedAt   time.Time  `json:"createdAt"`
	Status      string     `json:"status"` // pending | accepted | rejected
	Title       string     `json:"title"`
	Signal      string     `json:"signal"`    // correction | rediscovery | failure_fix | convention | workflow
	Scope       string     `json:"scope"`     // machine | repo
	Placement   string     `json:"placement"` // always_on | path_scoped | skill
	Sensitivity bool       `json:"sensitivity"`
	Confidence  float64    `json:"confidence"`
	Project     string     `json:"project"`
	RepoRoot    string     `json:"repoRoot"`
	TargetPath  string     `json:"targetPath"` // predicted (pending) or actual (accepted) destination, repo-relative
	Globs       string     `json:"globs"`      // comma-separated path globs for path_scoped placements
	Body        string     `json:"body"`
	Rationale   string     `json:"rationale"`
	Evidence    []Evidence `json:"evidence"`
	SessionID   string     `json:"sessionId"`
	Tool        string     `json:"tool"`
	WrittenPath string     `json:"writtenPath,omitempty"`
	// BlockID is the managed-block id this suggestion writes to. Empty means "own id" (new
	// skill). Gardener suggestions set it to an EXISTING block id to amend it — or, with an
	// empty Body, to prune it.
	BlockID string `json:"blockId,omitempty"`
}

func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "autoskills.db"
	}
	return filepath.Join(home, ".autoskills", "autoskills.db")
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS ingest_files (
  path            TEXT PRIMARY KEY,
  bytes_processed INTEGER NOT NULL DEFAULT 0,
  updated_at      TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS suggestions (
  id            TEXT PRIMARY KEY,
  created_at    TEXT NOT NULL,
  status        TEXT NOT NULL DEFAULT 'pending',
  title         TEXT NOT NULL,
  signal        TEXT NOT NULL DEFAULT '',
  scope         TEXT NOT NULL DEFAULT 'repo',
  placement     TEXT NOT NULL DEFAULT 'always_on',
  sensitivity   INTEGER NOT NULL DEFAULT 0,
  confidence    REAL NOT NULL DEFAULT 0,
  project       TEXT NOT NULL DEFAULT '',
  repo_root     TEXT NOT NULL DEFAULT '',
  target_path   TEXT NOT NULL DEFAULT '',
  globs         TEXT NOT NULL DEFAULT '',
  body          TEXT NOT NULL DEFAULT '',
  rationale     TEXT NOT NULL DEFAULT '',
  evidence_json TEXT NOT NULL DEFAULT '[]',
  session_id    TEXT NOT NULL DEFAULT '',
  tool          TEXT NOT NULL DEFAULT '',
  written_path  TEXT NOT NULL DEFAULT '',
  block_id      TEXT NOT NULL DEFAULT '',
  decided_at    TEXT
);
CREATE INDEX IF NOT EXISTS idx_suggestions_status ON suggestions(status);
`)
	if err != nil {
		return err
	}
	// additive migrations for databases created by older binaries; duplicate-column errors are fine
	_, _ = s.db.Exec(`ALTER TABLE suggestions ADD COLUMN globs TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.Exec(`ALTER TABLE suggestions ADD COLUMN block_id TEXT NOT NULL DEFAULT ''`)
	return nil
}

// BytesProcessed returns the high-water mark for a transcript file (0 if never seen).
func (s *Store) BytesProcessed(path string) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT bytes_processed FROM ingest_files WHERE path = ?`, path).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n, err
}

func (s *Store) SetBytesProcessed(path string, n int64) error {
	_, err := s.db.Exec(`INSERT INTO ingest_files(path, bytes_processed, updated_at) VALUES(?,?,?)
ON CONFLICT(path) DO UPDATE SET bytes_processed=excluded.bytes_processed, updated_at=excluded.updated_at`,
		path, n, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *Store) InsertSuggestion(g Suggestion) error {
	ev, err := json.Marshal(g.Evidence)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO suggestions
(id, created_at, status, title, signal, scope, placement, sensitivity, confidence, project, repo_root, target_path, globs, body, rationale, evidence_json, session_id, tool, block_id)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		g.ID, g.CreatedAt.UTC().Format(time.RFC3339), g.Status, g.Title, g.Signal, g.Scope, g.Placement,
		boolToInt(g.Sensitivity), g.Confidence, g.Project, g.RepoRoot, g.TargetPath, g.Globs, g.Body, g.Rationale,
		string(ev), g.SessionID, g.Tool, g.BlockID)
	return err
}

// TitleExists reports whether a suggestion with this exact title already exists for the repo —
// the cheap layer of dedupe beneath the prompt-level dedupe.
func (s *Store) TitleExists(repoRoot, title string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM suggestions WHERE repo_root = ? AND title = ?`, repoRoot, title).Scan(&n)
	return n > 0, err
}

// ExistingTitles returns all suggestion titles for a repo (any status) for prompt-level dedupe.
func (s *Store) ExistingTitles(repoRoot string) ([]string, error) {
	rows, err := s.db.Query(`SELECT title FROM suggestions WHERE repo_root = ?`, repoRoot)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ListSuggestions(status string) ([]Suggestion, error) {
	q := `SELECT id, created_at, status, title, signal, scope, placement, sensitivity, confidence,
project, repo_root, target_path, globs, body, rationale, evidence_json, session_id, tool, written_path, block_id
FROM suggestions`
	var args []any
	if status != "" && status != "all" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Suggestion
	for rows.Next() {
		g, err := scanSuggestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) GetSuggestion(id string) (Suggestion, error) {
	row := s.db.QueryRow(`SELECT id, created_at, status, title, signal, scope, placement, sensitivity, confidence,
project, repo_root, target_path, globs, body, rationale, evidence_json, session_id, tool, written_path, block_id
FROM suggestions WHERE id = ?`, id)
	return scanSuggestion(row)
}

// Decide records an accept/reject. For accepts, body may be the user-edited version and
// writtenPath records where the writer placed it.
func (s *Store) Decide(id, status, body, writtenPath string) error {
	res, err := s.db.Exec(`UPDATE suggestions SET status = ?, body = COALESCE(NULLIF(?, ''), body),
written_path = ?, decided_at = ? WHERE id = ?`,
		status, body, writtenPath, time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("suggestion %s not found", id)
	}
	return nil
}

type Stats struct {
	Pending  int `json:"pending"`
	Accepted int `json:"accepted"`
	Rejected int `json:"rejected"`
	Sessions int `json:"sessions"`
	Projects int `json:"projects"`
}

func (s *Store) Stats() (Stats, error) {
	var st Stats
	err := s.db.QueryRow(`SELECT
COUNT(CASE WHEN status='pending' THEN 1 END),
COUNT(CASE WHEN status='accepted' THEN 1 END),
COUNT(CASE WHEN status='rejected' THEN 1 END),
COUNT(DISTINCT session_id),
COUNT(DISTINCT project)
FROM suggestions`).Scan(&st.Pending, &st.Accepted, &st.Rejected, &st.Sessions, &st.Projects)
	return st, err
}

type ProjectCount struct {
	Name     string `json:"name"`
	RepoRoot string `json:"repoRoot"`
	Pending  int    `json:"pending"`
}

func (s *Store) Projects() ([]ProjectCount, error) {
	rows, err := s.db.Query(`SELECT project, repo_root, COUNT(CASE WHEN status='pending' THEN 1 END)
FROM suggestions GROUP BY project, repo_root ORDER BY project`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectCount
	for rows.Next() {
		var p ProjectCount
		if err := rows.Scan(&p.Name, &p.RepoRoot, &p.Pending); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanSuggestion(r rowScanner) (Suggestion, error) {
	var g Suggestion
	var created, evidenceJSON string
	var sens int
	err := r.Scan(&g.ID, &created, &g.Status, &g.Title, &g.Signal, &g.Scope, &g.Placement, &sens,
		&g.Confidence, &g.Project, &g.RepoRoot, &g.TargetPath, &g.Globs, &g.Body, &g.Rationale, &evidenceJSON,
		&g.SessionID, &g.Tool, &g.WrittenPath, &g.BlockID)
	if err != nil {
		return g, err
	}
	g.Sensitivity = sens != 0
	g.CreatedAt, _ = time.Parse(time.RFC3339, created)
	_ = json.Unmarshal([]byte(evidenceJSON), &g.Evidence)
	if g.Evidence == nil {
		g.Evidence = []Evidence{} // never serialize null to the frontend
	}
	return g, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
