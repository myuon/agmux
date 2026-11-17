package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/myuon/agmux/internal/experimental"
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
	writeJSON(w, http.StatusOK, map[string]interface{}{"features": experimental.List()})
}
