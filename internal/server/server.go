// Package server exposes the local review API and serves the embedded dashboard. Binds to
// 127.0.0.1 only — this is the "web dashboard with no server" (PRD §10.2).
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/elcruzo/autoskills/internal/store"
	"github.com/elcruzo/autoskills/internal/writer"
	"github.com/elcruzo/autoskills/web"
)

const DefaultAddr = "127.0.0.1:4517"

type Server struct {
	Store *store.Store
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/suggestions", s.handleListSuggestions)
	mux.HandleFunc("POST /api/suggestions/{id}/decision", s.handleDecision)
	mux.HandleFunc("GET /api/projects", s.handleProjects)
	mux.Handle("/", uiHandler())
	return mux
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.Stats()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, st)
}

func (s *Server) handleListSuggestions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}
	list, err := s.Store.ListSuggestions(status)
	if err != nil {
		writeErr(w, err)
		return
	}
	if list == nil {
		list = []store.Suggestion{}
	}
	writeJSON(w, map[string]any{"suggestions": list})
}

type decisionRequest struct {
	Action string `json:"action"` // accept | reject
	Body   string `json:"body"`   // optional edited markdown (accept only)
}

func (s *Server) handleDecision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req decisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	g, err := s.Store.GetSuggestion(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if g.Status != "pending" {
		http.Error(w, "suggestion is no longer pending", http.StatusConflict)
		return
	}

	switch req.Action {
	case "reject":
		// the status check above is a courtesy: Reject is a compare-and-set from pending inside a
		// transaction, so a rejection racing an acceptance loses instead of contradicting it
		if err := s.Store.Reject(id); err != nil {
			writeDecisionErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	case "accept":
		if req.Body != "" {
			g.Body = req.Body
		}
		// The plan is validated on the body that will actually be written, edits included. A
		// suggestion whose artifact plan does not resolve is a client error: nothing is written
		// and the decision is not recorded.
		if _, err := writer.BuildPlan(g); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Accept journals the mutation, writes the files, then records the decision in the same
		// transaction that closes the journal entry — the status above is a courtesy 409, the
		// binding pending-only check happens inside that transaction.
		written, err := writer.Accept(s.Store, g)
		if err != nil {
			writeDecisionErr(w, fmt.Errorf("write skill: %w", err))
			return
		}
		writeJSON(w, map[string]any{"ok": true, "writtenPath": written})
	default:
		http.Error(w, "action must be accept or reject", http.StatusBadRequest)
	}
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.Store.Projects()
	if err != nil {
		writeErr(w, err)
		return
	}
	if projects == nil {
		projects = []store.ProjectCount{}
	}
	writeJSON(w, map[string]any{"projects": projects})
}

// uiHandler serves the embedded dashboard build with an index.html SPA fallback.
func uiHandler() http.Handler {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return placeholderHandler()
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return placeholderHandler()
	}
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if strings.HasPrefix(p, "api/") {
			http.NotFound(w, r) // unknown/wrong-method API routes must not get 200 HTML
			return
		}
		if p != "" {
			if _, err := fs.Stat(dist, p); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

func placeholderHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "autoskills: dashboard assets not built. run `bun run build` in web/ and rebuild the binary.")
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// writeDecisionErr separates "you lost a race" from "something broke". Both clients of a contested
// suggestion must be able to tell those apart: one reloads and decides again, the other has a bug
// or a broken database to report. Losing a compare-and-set, hitting an unfinished operation, and
// finding a destination changed underneath a manifest are all the first kind.
func writeDecisionErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotPending),
		errors.Is(err, store.ErrOperationInFlight),
		errors.Is(err, store.ErrResourceBusy),
		errors.Is(err, writer.ErrConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		writeErr(w, err)
	}
}
