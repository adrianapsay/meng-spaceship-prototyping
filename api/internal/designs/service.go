// Package designs owns the design lifecycle: create, run the agent in the
// background, persist results, and publish events.
package designs

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/agent"
	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/cad"
	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/events"
	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/llm"
	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/store"
)

const (
	StatusRunning = "running"
	StatusPassed  = "passed"
	StatusFailed  = "failed"

	EventStarted   = "design.started"
	EventRetrying  = "agent.retrying"
	EventCompleted = "design.completed"

	runTimeout = 15 * time.Minute
)

type Service struct {
	q         *store.Queries
	hub       *events.Hub
	agent     *agent.Agent
	providers *llm.Registry
	log       *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewService(q *store.Queries, hub *events.Hub, cadClient agent.CAD, providers *llm.Registry, log *slog.Logger) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{q: q, hub: hub, providers: providers, log: log, ctx: ctx, cancel: cancel}
	s.agent = &agent.Agent{CAD: cadClient, Recorder: s}
	return s
}

type CreateParams struct {
	Prompt        string
	Provider      string
	Model         string
	MaxIterations int
}

// Create stores a new design and starts the agent in the background.
func (s *Service) Create(ctx context.Context, p CreateParams) (store.Design, error) {
	client, provider, model, err := s.providers.Resolve(p.Provider, p.Model)
	if err != nil {
		return store.Design{}, err
	}
	d, err := s.q.CreateDesign(ctx, store.CreateDesignParams{Prompt: p.Prompt, Provider: provider, Model: model})
	if err != nil {
		return store.Design{}, err
	}
	s.wg.Add(1)
	go s.run(d, agent.Job{DesignID: d.ID, Prompt: p.Prompt, LLM: client, MaxIterations: p.MaxIterations})
	return d, nil
}

func (s *Service) run(d store.Design, job agent.Job) {
	defer s.wg.Done()
	ctx, cancel := context.WithTimeout(s.ctx, runTimeout)
	defer cancel()
	log := s.log.With("design_id", d.ID, "provider", d.Provider, "model", d.Model)
	log.Info("design started")
	_ = s.Emit(ctx, d.ID, EventStarted, map[string]string{"provider": d.Provider, "model": d.Model})
	ctx = llm.WithRetryHook(ctx, func(info llm.RetryInfo) {
		log.Warn("llm retry", "attempt", info.Attempt, "wait_s", info.WaitSeconds, "reason", info.Reason)
		_ = s.Emit(ctx, d.ID, EventRetrying, info)
	})

	res, err := s.agent.Run(ctx, job)

	// Record the outcome even if the run context was cancelled by shutdown.
	done, cancelDone := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDone()
	status, errMsg := StatusPassed, (*string)(nil)
	if err != nil || !res.Passed {
		status = StatusFailed
		if err != nil {
			msg := err.Error()
			errMsg = &msg
		}
	}
	summary := &res.Summary
	if res.Summary == "" {
		summary = nil
	}
	if ferr := s.q.FinishDesign(done, store.FinishDesignParams{ID: d.ID, Status: status, Summary: summary, Error: errMsg}); ferr != nil {
		log.Error("finish design", "err", ferr)
	}
	_ = s.Emit(done, d.ID, EventCompleted, map[string]any{
		"status": status, "summary": summary, "error": errMsg, "final_iteration": res.FinalIteration,
	})
	log.Info("design finished", "status", status, "iterations", res.FinalIteration, "err", err)
}

// Close stops running designs and waits for them to record their outcome.
func (s *Service) Close() {
	s.cancel()
	s.wg.Wait()
}

// SaveIteration implements agent.Recorder.
func (s *Service) SaveIteration(ctx context.Context, designID uuid.UUID, n int, spec json.RawMessage, report *cad.Report) error {
	if !json.Valid(spec) {
		spec, _ = json.Marshal(string(spec))
	}
	return s.q.CreateIteration(ctx, store.CreateIterationParams{
		DesignID: designID, N: int32(n), Spec: spec, Report: report.Raw, Passed: report.Passed,
	})
}

// Emit implements agent.Recorder: append to the event log, then notify subscribers.
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
