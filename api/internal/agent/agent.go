// Package agent runs the design loop: the model proposes an AssemblySpec, the
// CAD service builds and checks it, and the model revises until it passes.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/cad"
	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/llm"
)

// ArtifactsURLPrefix is where the HTTP API serves files from the artifacts root.
const ArtifactsURLPrefix = "/v1/artifacts/"

const (
	toolBuild    = "build_and_validate"
	toolFinalize = "finalize"
	maxNudges    = 2
)

// CAD is the part of the CAD client the agent needs.
type CAD interface {
	Schema(ctx context.Context) (json.RawMessage, error)
	Catalog(ctx context.Context) (json.RawMessage, error)
	Build(ctx context.Context, spec json.RawMessage, artifactDir string) (*cad.Report, error)
}

// Recorder persists iterations and publishes progress events.
type Recorder interface {
	SaveIteration(ctx context.Context, designID uuid.UUID, n int, spec json.RawMessage, report *cad.Report) error
	Emit(ctx context.Context, designID uuid.UUID, eventType string, data any) error
}

type Job struct {
	DesignID      uuid.UUID
	Prompt        string
	LLM           llm.Client
	MaxIterations int
}

type Result struct {
	Passed         bool
	Summary        string
	FinalIteration int
}

type Agent struct {
	CAD      CAD
	Recorder Recorder
}

// IterationEvent is the payload of "iteration.completed" events.
type IterationEvent struct {
	N       int             `json:"n"`
	Passed  bool            `json:"passed"`
	Issues  []cad.Issue     `json:"issues"`
	Metrics json.RawMessage `json:"metrics,omitempty"`
	GLBURL  string          `json:"glb_url,omitempty"`
	STEPURL string          `json:"step_url,omitempty"`
}

func NewIterationEvent(n int, r *cad.Report) IterationEvent {
	ev := IterationEvent{N: n, Passed: r.Passed, Issues: r.Issues, Metrics: r.Metrics}
	if r.Artifacts != nil {
		ev.GLBURL = ArtifactsURLPrefix + r.Artifacts.GLB
		ev.STEPURL = ArtifactsURLPrefix + r.Artifacts.STEP
	}
	return ev
}

func (a *Agent) Run(ctx context.Context, job Job) (Result, error) {
	schema, err := a.CAD.Schema(ctx)
	if err != nil {
		return Result{}, err
	}
	catalog, err := a.CAD.Catalog(ctx)
	if err != nil {
		return Result{}, err
	}
	tools := []llm.Tool{
		{
			Name: toolBuild,
			Description: "Build the assembly in CAD and run engineering checks (interference, " +
				"envelope, mounting, mass, center of mass). Returns a report with every issue " +
				"and each part's bounding box. The argument is the full AssemblySpec.",
			Parameters: schema,
		},
		{
			Name:        toolFinalize,
			Description: "Finish the design. Only allowed after the latest build passed.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string","description":"2-4 sentence summary of the final design and how it meets the request."}},"required":["summary"]}`),
		},
	}
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt(catalog, job.MaxIterations)},
		{Role: llm.RoleUser, Content: job.Prompt},
	}

	var (
		builds, nudges int
		lastPassed     bool
		lastPassing    int // most recent iteration that passed every check
	)
	// stop ends a run that was not explicitly finalized. A passing design is
	// kept rather than discarded because of a later failure (e.g. a provider outage).
	stop := func(reason error) (Result, error) {
		if lastPassing == 0 {
			return Result{FinalIteration: builds}, reason
		}
		return Result{
			Passed:         true,
			FinalIteration: lastPassing,
			Summary:        fmt.Sprintf("Stopped early (%s). Kept iteration %d, the latest passing design.", brief(reason), lastPassing),
		}, nil
	}

	for turn := 0; turn < 3*job.MaxIterations+maxNudges+2; turn++ {
		reply, err := job.LLM.Chat(ctx, messages, tools)
		if err != nil {
			return stop(fmt.Errorf("llm: %w", err))
		}
		messages = append(messages, reply)
		if reply.Content != "" {
			a.emit(ctx, job.DesignID, "agent.message", map[string]string{"text": reply.Content})
		}

		if len(reply.ToolCalls) == 0 {
			if lastPassed {
				return Result{Passed: true, Summary: reply.Content, FinalIteration: lastPassing}, nil
			}
			if nudges++; nudges > maxNudges {
				return stop(fmt.Errorf("model stopped calling tools"))
			}
			messages = append(messages, llm.Message{Role: llm.RoleUser, Content: "Continue: call " + toolBuild + " with a complete AssemblySpec."})
			continue
		}

		var finalSummary *string
		for _, call := range reply.ToolCalls {
			var result string
			switch call.Name {
			case toolBuild:
				if builds >= job.MaxIterations {
					result = "Build budget exhausted."
					break
				}
				builds++
				report, err := a.build(ctx, job.DesignID, builds, call.Arguments)
				if err != nil {
					return stop(err)
				}
				lastPassed = report.Passed
				if report.Passed {
					lastPassing = builds
				}
				result = toolResult(report, job.MaxIterations-builds)
			case toolFinalize:
				var args struct {
					Summary string `json:"summary"`
				}
				_ = json.Unmarshal(call.Arguments, &args)
				if !lastPassed {
					result = "Cannot finalize: the latest build has errors. Fix them and rebuild."
					break
				}
				finalSummary = &args.Summary
				result = "Design finalized."
			default:
				result = fmt.Sprintf("Unknown tool %q. Available: %s, %s.", call.Name, toolBuild, toolFinalize)
			}
			messages = append(messages, llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: result})
		}

		if finalSummary != nil {
			return Result{Passed: true, Summary: *finalSummary, FinalIteration: lastPassing}, nil
		}
		if builds >= job.MaxIterations {
			return stop(fmt.Errorf("no passing design after %d builds", builds))
		}
	}
	return stop(fmt.Errorf("turn limit reached"))
}

func (a *Agent) build(ctx context.Context, designID uuid.UUID, n int, spec json.RawMessage) (*cad.Report, error) {
	a.emit(ctx, designID, "iteration.started", map[string]int{"n": n})
	report, err := a.CAD.Build(ctx, spec, fmt.Sprintf("%s/iter_%d", designID, n))
	if err != nil {
		return nil, err
	}
	if err := a.Recorder.SaveIteration(ctx, designID, n, spec, report); err != nil {
		return nil, err
	}
	a.emit(ctx, designID, "iteration.completed", NewIterationEvent(n, report))
	return report, nil
}

// emit is best-effort: a failed progress event should not abort the design.
func (a *Agent) emit(ctx context.Context, designID uuid.UUID, eventType string, data any) {
	_ = a.Recorder.Emit(ctx, designID, eventType, data)
}

func toolResult(r *cad.Report, buildsLeft int) string {
	next := fmt.Sprintf("Fix every error and call %s again (%d builds left).", toolBuild, buildsLeft)
	if r.Passed {
		next = fmt.Sprintf("All checks passed. Now re-read the user's request and list each explicit requirement "+
			"(payload, deployables, power, pointing, etc.). If every one is met, call %s immediately. "+
			"Otherwise change the spec to meet it and rebuild. Never resubmit an identical spec.", toolFinalize)
	}
	return string(r.Raw) + "\n\n" + next
}

// brief shortens an error to its first sentence for user-facing summaries.
func brief(err error) string {
	msg := err.Error()
	if i := strings.IndexAny(msg, ".\n"); i > 0 {
		msg = msg[:i]
	}
	if len(msg) > 120 {
		msg = msg[:120] + "…"
	}
	return msg
}
