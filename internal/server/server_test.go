package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elcruzo/autoskills/internal/store"
)

func newTestServer(t *testing.T) (*Server, *store.Store, string) {
	t.Helper()
	repo := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	err = st.InsertSuggestion(store.Suggestion{
		ID: "sg_int01", CreatedAt: time.Now(), Status: "pending",
		Title: "Use pnpm, never npm", Signal: "convention", Scope: "repo", Placement: "always_on",
		Confidence: 0.9, Project: "demo", RepoRoot: repo,
		Body: "- always pnpm", Rationale: "stated twice",
		Evidence: []store.Evidence{{Excerpt: "no, use pnpm", SessionID: "s1", Tool: "cursor"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &Server{Store: st}, st, repo
}

func TestListAcceptFlow(t *testing.T) {
	srv, st, repo := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// list pending
	resp, err := ts.Client().Get(ts.URL + "/api/suggestions?status=pending")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Suggestions []store.Suggestion `json:"suggestions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(list.Suggestions) != 1 || list.Suggestions[0].ID != "sg_int01" {
		t.Fatalf("unexpected list: %+v", list.Suggestions)
	}
	if len(list.Suggestions[0].Evidence) != 1 {
		t.Fatalf("evidence lost in round-trip: %+v", list.Suggestions[0])
	}

	// accept with an edited body
	body := strings.NewReader(`{"action":"accept","body":"- EDITED: always pnpm"}`)
	resp, err = ts.Client().Post(ts.URL+"/api/suggestions/sg_int01/decision", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	var dec struct {
		OK          bool   `json:"ok"`
		WrittenPath string `json:"writtenPath"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dec); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !dec.OK || dec.WrittenPath == "" {
		t.Fatalf("bad decision response: %+v", dec)
	}

	// file actually written with the edited body inside managed markers
	raw, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "EDITED: always pnpm") || !strings.Contains(string(raw), "autoskills:begin id=sg_int01") {
		t.Fatalf("AGENTS.md content wrong:\n%s", raw)
	}

	// store reflects the decision
	g, err := st.GetSuggestion("sg_int01")
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != "accepted" || g.WrittenPath == "" || !strings.Contains(g.Body, "EDITED") {
		t.Fatalf("store not updated: %+v", g)
	}

	// stats see it
	stats, err := st.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Accepted != 1 || stats.Pending != 0 {
		t.Fatalf("stats wrong: %+v", stats)
	}
}

func TestRejectFlow(t *testing.T) {
	srv, st, repo := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"/api/suggestions/sg_int01/decision", "application/json",
		strings.NewReader(`{"action":"reject"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	g, _ := st.GetSuggestion("sg_int01")
	if g.Status != "rejected" {
		t.Fatalf("status = %s", g.Status)
	}
	if _, err := os.Stat(filepath.Join(repo, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("reject must not write files")
	}
}

func TestUnknownActionRejected(t *testing.T) {
	srv, _, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"/api/suggestions/sg_int01/decision", "application/json",
		strings.NewReader(`{"action":"yolo"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}
