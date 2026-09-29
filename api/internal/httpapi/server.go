// Package httpapi is the public REST + SSE API.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/designs"
	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/events"
	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/store"
)

const (
	maxPromptChars       = 2000
	defaultMaxIterations = 6
	maxMaxIterations     = 12
	sseHeartbeat         = 15 * time.Second
)

func init() {
	_ = mime.AddExtensionType(".glb", "model/gltf-binary")
	_ = mime.AddExtensionType(".step", "model/step")
}

type Server struct {
	Queries       *store.Queries
	Designs       *designs.Service
	Hub           *events.Hub
	ArtifactsRoot string
	CORSOrigin    string
	Ping          func(context.Context) error
	Log           *slog.Logger
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /v1/providers", s.listProviders)
	mux.HandleFunc("POST /v1/designs", s.createDesign)
	mux.HandleFunc("GET /v1/designs", s.listDesigns)
	mux.HandleFunc("GET /v1/designs/{id}", s.getDesign)
	mux.HandleFunc("GET /v1/designs/{id}/events", s.streamEvents)
	mux.Handle("GET "+designs.ArtifactsURLPrefix, http.StripPrefix(designs.ArtifactsURLPrefix,
		http.FileServerFS(os.DirFS(s.ArtifactsRoot))))
	return s.recoverer(s.logRequests(s.cors(mux)))
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "database unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	p, err := s.Designs.Providers(r.Context())
	if err != nil {
		s.Log.Error("list providers", "err", err)
		writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "agent service unavailable")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type createDesignRequest struct {
	Prompt        string `json:"prompt"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	MaxIterations int    `json:"max_iterations"`
}

func (s *Server) createDesign(w http.ResponseWriter, r *http.Request) {
	var req createDesignRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	switch {
	case req.Prompt == "":
		writeError(w, http.StatusBadRequest, "invalid_request", "prompt is required")
		return
	case utf8.RuneCountInString(req.Prompt) > maxPromptChars:
		writeError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("prompt exceeds %d characters", maxPromptChars))
		return
	case req.MaxIterations < 0 || req.MaxIterations > maxMaxIterations:
		writeError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("max_iterations must be 1-%d", maxMaxIterations))
		return
	}
	if req.MaxIterations == 0 {
		req.MaxIterations = defaultMaxIterations
	}

	d, err := s.Designs.Create(r.Context(), designs.CreateParams{
		Prompt: req.Prompt, Provider: req.Provider, Model: req.Model, MaxIterations: req.MaxIterations,
	})
	if errors.Is(err, designs.ErrUnknownProvider) {
		writeError(w, http.StatusBadRequest, "invalid_provider", err.Error())
		return
	}
	if errors.Is(err, designs.ErrAgentUnavailable) {
		s.Log.Error("create design", "err", err)
		writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "agent service unavailable")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/designs/"+d.ID.String())
	writeJSON(w, http.StatusAccepted, d)
}

func (s *Server) listDesigns(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Queries.ListDesigns(r.Context(), 50)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if rows == nil {
		rows = []store.ListDesignsRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"designs": rows})
}

type iterationDTO struct {
	N         int32           `json:"n"`
	Passed    bool            `json:"passed"`
	Spec      json.RawMessage `json:"spec"`
	Report    json.RawMessage `json:"report"`
	GLBURL    string          `json:"glb_url,omitempty"`
	STEPURL   string          `json:"step_url,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

func (s *Server) getDesign(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designID(w, r)
	if !ok {
		return
	}
	d, err := s.Queries.GetDesign(r.Context(), id)
	if err != nil {
		s.lookupError(w, r, err)
		return
	}
	rows, err := s.Queries.ListIterations(r.Context(), id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	iterations := make([]iterationDTO, len(rows))
	for i, it := range rows {
		iterations[i] = iterationDTO{N: it.N, Passed: it.Passed, Spec: it.Spec, Report: it.Report, CreatedAt: it.CreatedAt}
		var rep struct {
			Artifacts *struct{ GLB, STEP string } `json:"artifacts"`
		}
		if json.Unmarshal(it.Report, &rep) == nil && rep.Artifacts != nil {
			iterations[i].GLBURL = designs.ArtifactsURLPrefix + rep.Artifacts.GLB
			iterations[i].STEPURL = designs.ArtifactsURLPrefix + rep.Artifacts.STEP
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"design": d, "iterations": iterations})
}

// streamEvents replays the design's event log, then streams live events
// until the design completes. Clients resume with Last-Event-ID.
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designID(w, r)
	if !ok {
		return
	}
	if _, err := s.Queries.GetDesign(r.Context(), id); err != nil {
		s.lookupError(w, r, err)
		return
	}
	var lastID int64
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		lastID, _ = strconv.ParseInt(v, 10, 64)
	}

	// Subscribe before replaying so nothing is missed in between.
	live, unsubscribe := s.Hub.Subscribe(id)
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	send := func(ev store.Event) error {
		lastID = ev.ID
		if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, ev.Data); err != nil {
			return err
		}
		return rc.Flush()
	}

	past, err := s.Queries.ListEventsAfter(r.Context(), store.ListEventsAfterParams{DesignID: id, ID: lastID})
	if err != nil {
		s.Log.Error("replay events", "err", err)
		return
	}
	for _, ev := range past {
		if send(ev) != nil || ev.Type == designs.EventCompleted {
			return
		}
	}
	// A client resuming after completion has nothing left to wait for.
	if d, err := s.Queries.GetDesign(r.Context(), id); err != nil || d.Status != designs.StatusRunning {
		return
	}

	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		case ev, open := <-live:
			if !open {
				return // fell behind; the client reconnects with Last-Event-ID
			}
			if ev.ID <= lastID {
				continue
			}
			if send(ev) != nil || ev.Type == designs.EventCompleted {
				return
			}
		}
	}
}

func (s *Server) designID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "design not found")
		return uuid.Nil, false
	}
	return id, true
}

func (s *Server) lookupError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "design not found")
		return
	}
	s.internalError(w, r, err)
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal server error")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
