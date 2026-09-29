// Package designs owns the design lifecycle: create, run the agent service in
// the background, persist what it reports, and publish events.
package designs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/agentclient"
	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/events"
	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/store"
)

const (
	StatusRunning = "running"
	StatusPassed  = "passed"
	StatusFailed  = "failed"

	EventStarted   = "design.started"
	EventCompleted = "design.completed"

	// ArtifactsURLPrefix is where the HTTP API serves files from the artifacts root.
	ArtifactsURLPrefix = "/v1/artifacts/"

	runTimeout = 15 * time.Minute
)

var (
	ErrUnknownProvider  = errors.New("unknown or unconfigured provider")
	ErrAgentUnavailable = errors.New("agent service unavailable")
)

// Agent is the part of the agent service client this package needs.
type Agent interface {
	Providers(ctx context.Context) (agentclient.Providers, error)
	Run(ctx context.Context, req agentclient.RunRequest, fn func(agentclient.Event) error) error
}

type Service struct {
	q     *store.Queries
	hub   *events.Hub
	agent Agent
	log   *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewService(q *store.Queries, hub *events.Hub, agent Agent, log *slog.Logger) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{q: q, hub: hub, agent: agent, log: log, ctx: ctx, cancel: cancel}
}

// Providers lists the LLM providers the agent service has configured.
func (s *Service) Providers(ctx context.Context) (agentclient.Providers, error) {
	p, err := s.agent.Providers(ctx)
	if err != nil {
		return p, fmt.Errorf("%w: %v", ErrAgentUnavailable, err)
	}
	return p, nil
}

type CreateParams struct {
	Prompt        string
	Provider      string
	Model         string
	MaxIterations int
}

// Create stores a new design and starts the agent in the background.
func (s *Service) Create(ctx context.Context, p CreateParams) (store.Design, error) {
	provider, model, err := s.resolve(ctx, p.Provider, p.Model)
	if err != nil {
		return store.Design{}, err
	}
	d, err := s.q.CreateDesign(ctx, store.CreateDesignParams{Prompt: p.Prompt, Provider: provider, Model: model})
	if err != nil {
		return store.Design{}, err
	}
	s.wg.Add(1)
	go s.run(d, agentclient.RunRequest{
		DesignID: d.ID.String(), Prompt: p.Prompt, Provider: provider, Model: model, MaxIterations: p.MaxIterations,
	})
	return d, nil
}

// resolve fills in the default provider and model, so each design records exactly what ran it.
func (s *Service) resolve(ctx context.Context, provider, model string) (string, string, error) {
	ps, err := s.Providers(ctx)
	if err != nil {
		return "", "", err
	}
	if provider == "" {
		provider = ps.Default
	}
	for _, p := range ps.Providers {
		if p.Name == provider {
			if model == "" {
				model = p.DefaultModel
			}
			return provider, model, nil
		}
	}
	if provider == "" {
		return "", "", fmt.Errorf("%w: no LLM provider configured (set GEMINI_API_KEY)", ErrUnknownProvider)
	}
	return "", "", fmt.Errorf("%w: %q", ErrUnknownProvider, provider)
}

// outcome is the "run.finished" payload from the agent service.
type outcome struct {
	Passed         bool    `json:"passed"`
	FinalIteration int     `json:"final_iteration"`
	Summary        *string `json:"summary"`
	Error          *string `json:"error"`
}

func (s *Service) run(d store.Design, req agentclient.RunRequest) {
	defer s.wg.Done()
	ctx, cancel := context.WithTimeout(s.ctx, runTimeout)
	defer cancel()
	log := s.log.With("design_id", d.ID, "provider", d.Provider, "model", d.Model)
	log.Info("design started")
	_ = s.Emit(ctx, d.ID, EventStarted, map[string]string{"provider": d.Provider, "model": d.Model})

	var (
		out         *outcome
		lastPassing int
	)
	err := s.agent.Run(ctx, req, func(ev agentclient.Event) error {
		switch ev.Event {
		case "iteration.completed":
			n, passed, err := s.saveIteration(ctx, d.ID, ev.Data)
			if err != nil {
				return err
			}
			if passed {
				lastPassing = n
			}
		case "run.finished":
			out = &outcome{}
			return json.Unmarshal(ev.Data, out)
		default: // progress events go straight to the browser
			if ev.Event == "agent.retrying" {
				log.Warn("llm retry", "info", string(ev.Data))
			}
			_ = s.Emit(ctx, d.ID, ev.Event, ev.Data)
		}
		return nil
	})
	if out == nil {
		// The stream broke (agent crash, timeout or shutdown) before the run finished.
		out = interrupted(err, lastPassing)
	}

	// Record the outcome even if the run context was cancelled by shutdown.
	done, cancelDone := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDone()
	status := StatusFailed
	if out.Passed {
		status = StatusPassed
	}
	if ferr := s.q.FinishDesign(done, store.FinishDesignParams{ID: d.ID, Status: status, Summary: out.Summary, Error: out.Error}); ferr != nil {
		log.Error("finish design", "err", ferr)
	}
	_ = s.Emit(done, d.ID, EventCompleted, map[string]any{
		"status": status, "summary": out.Summary, "error": out.Error, "final_iteration": out.FinalIteration,
	})
	log.Info("design finished", "status", status, "iterations", out.FinalIteration)
}

func interrupted(err error, lastPassing int) *outcome {
	reason := "agent stream ended before the run finished"
	if err != nil {
		reason = err.Error()
	}
	if lastPassing == 0 {
		return &outcome{FinalIteration: 0, Error: &reason}
	}
	summary := fmt.Sprintf("Stopped early (%s). Kept iteration %d, the latest passing design.", reason, lastPassing)
	return &outcome{Passed: true, FinalIteration: lastPassing, Summary: &summary}
}

// iterationEvent is the browser-facing payload of "iteration.completed":
// the report without the (large) spec, plus artifact URLs.
type iterationEvent struct {
	N       int             `json:"n"`
	Passed  bool            `json:"passed"`
	Issues  json.RawMessage `json:"issues"`
	Metrics json.RawMessage `json:"metrics,omitempty"`
	GLBURL  string          `json:"glb_url,omitempty"`
	STEPURL string          `json:"step_url,omitempty"`
}

// saveIteration persists an iteration from the agent stream and forwards a
// slimmed event to the browser.
func (s *Service) saveIteration(ctx context.Context, designID uuid.UUID, data json.RawMessage) (int, bool, error) {
	var it struct {
		N      int             `json:"n"`
		Spec   json.RawMessage `json:"spec"`
		Report json.RawMessage `json:"report"`
	}
	if err := json.Unmarshal(data, &it); err != nil {
		return 0, false, fmt.Errorf("bad iteration event: %w", err)
	}
	var rep struct {
		Passed    bool            `json:"passed"`
		Issues    json.RawMessage `json:"issues"`
		Metrics   json.RawMessage `json:"metrics"`
		Artifacts *struct {
			GLB  string `json:"glb"`
			STEP string `json:"step"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(it.Report, &rep); err != nil {
		return 0, false, fmt.Errorf("bad iteration report: %w", err)
	}
	if len(it.Spec) == 0 {
		it.Spec = json.RawMessage("null")
	}
	if err := s.q.CreateIteration(ctx, store.CreateIterationParams{
		DesignID: designID, N: int32(it.N), Spec: it.Spec, Report: it.Report, Passed: rep.Passed,
	}); err != nil {
		return 0, false, err
	}

	ev := iterationEvent{N: it.N, Passed: rep.Passed, Issues: rep.Issues, Metrics: rep.Metrics}
	if len(ev.Issues) == 0 || string(ev.Issues) == "null" {
		ev.Issues = json.RawMessage("[]")
	}
	if rep.Artifacts != nil {
		ev.GLBURL = ArtifactsURLPrefix + rep.Artifacts.GLB
		ev.STEPURL = ArtifactsURLPrefix + rep.Artifacts.STEP
	}
	_ = s.Emit(ctx, designID, "iteration.completed", ev)
	return it.N, rep.Passed, nil
}

// Close stops running designs and waits for them to record their outcome.
func (s *Service) Close() {
	s.cancel()
	s.wg.Wait()
}

// Emit appends to the event log, then notifies live subscribers.
func (s *Service) Emit(ctx context.Context, designID uuid.UUID, eventType string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	ev, err := s.q.InsertEvent(ctx, store.InsertEventParams{DesignID: designID, Type: eventType, Data: payload})
	if err != nil {
		s.log.Error("insert event", "design_id", designID, "type", eventType, "err", err)
		return err
	}
	s.hub.Publish(ev)
	return nil
}
