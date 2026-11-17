package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/experimental"
	"github.com/myuon/agmux/internal/search"
)

func (s *Server) listExperimental(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"features": experimental.List()})
}

func (s *Server) setExperimental(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, ok := experimental.Lookup(name); !ok {
		writeError(w, http.StatusNotFound, "unknown experimental feature: "+name)
		return
	}
	if err := experimental.Set(name, req.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if name == search.FeatureName && req.Enabled && s.searchIndexer != nil {
		s.searchIndexer.Kick()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"features": experimental.List()})
}

func (s *Server) initSearch() {
	if s.sqlDB == nil {
		return
	}
	streamsDir, err := db.StreamsDir()
	if err != nil {
		s.logger.Warn("search: streams dir unavailable", "error", err)
		return
	}
	ix, err := search.New(s.sqlDB, streamsDir, func() bool { return experimental.Enabled(search.FeatureName) }, s.logger)
	if err != nil {
		s.logger.Warn("search: init failed", "error", err)
		return
	}
	s.searchIndexer = ix
	go ix.Run(context.Background(), time.Minute)
}

func (s *Server) searchSessions(w http.ResponseWriter, r *http.Request) {
	if !experimental.Enabled(search.FeatureName) || s.searchIndexer == nil {
		writeError(w, http.StatusNotFound, "session_search is disabled")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	results, err := s.searchIndexer.Search(search.Query{
		Text:           q.Get("q"),
		ProjectPath:    q.Get("project"),
		ExcludeSession: q.Get("exclude"),
		Limit:          limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"results":  results,
		"indexing": s.searchIndexer.Indexing(),
	})
}
