package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/cad"
	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/llm"
)

// scriptedLLM replays a fixed sequence of assistant replies and records what it was sent.
type scriptedLLM struct {
	replies []llm.Message
	seen    [][]llm.Message
	errAt   int // 1-based call number that returns an error; 0 = never
}

func (s *scriptedLLM) Chat(_ context.Context, msgs []llm.Message, _ []llm.Tool) (llm.Message, error) {
	s.seen = append(s.seen, append([]llm.Message(nil), msgs...))
	if s.errAt == len(s.seen) {
		return llm.Message{}, errors.New("503 high demand")
	}
	if len(s.replies) == 0 {
		return llm.Message{Role: llm.RoleAssistant, Content: "done"}, nil
	}
	r := s.replies[0]
	s.replies = s.replies[1:]
	return r, nil
}

// fakeCAD passes a spec only if it contains `"ok": true`.
type fakeCAD struct{ dirs []string }

func (f *fakeCAD) Schema(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"type":"object"}`), nil
}

func (f *fakeCAD) Catalog(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"components":[]}`), nil
}

func (f *fakeCAD) Build(_ context.Context, spec json.RawMessage, dir string) (*cad.Report, error) {
	f.dirs = append(f.dirs, dir)
	var s struct {
		OK bool `json:"ok"`
	}
	_ = json.Unmarshal(spec, &s)
	r := &cad.Report{Passed: s.OK, Artifacts: &cad.Artifacts{GLB: dir + "/model.glb", STEP: dir + "/model.step"}}
	if !s.OK {
		r.Issues = []cad.Issue{{Code: "interference", Message: "a and b overlap by 10 mm^3", Severity: "error"}}
	}
	r.Raw, _ = json.Marshal(r)
	return r, nil
}

type memRecorder struct {
	iterations []int
	events     []string
}

func (m *memRecorder) SaveIteration(_ context.Context, _ uuid.UUID, n int, _ json.RawMessage, _ *cad.Report) error {
	m.iterations = append(m.iterations, n)
	return nil
}

func (m *memRecorder) Emit(_ context.Context, _ uuid.UUID, t string, _ any) error {
	m.events = append(m.events, t)
	return nil
}

func call(id, name, args string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: name, Arguments: json.RawMessage(args)}}}
}

func newAgent() (*Agent, *fakeCAD, *memRecorder) {
	c, rec := &fakeCAD{}, &memRecorder{}
	return &Agent{CAD: c, Recorder: rec}, c, rec
}

func TestRunFixesFailingBuildThenFinalizes(t *testing.T) {
	a, c, rec := newAgent()
	model := &scriptedLLM{replies: []llm.Message{
		call("1", toolBuild, `{"ok": false}`),
		call("2", toolBuild, `{"ok": true}`),
		call("3", toolFinalize, `{"summary": "a fine satellite"}`),
	}}
	id := uuid.New()
	res, err := a.Run(context.Background(), Job{DesignID: id, Prompt: "3U imager", LLM: model, MaxIterations: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Summary != "a fine satellite" || res.FinalIteration != 2 {
		t.Fatalf("unexpected result %+v", res)
	}
	if len(rec.iterations) != 2 || c.dirs[1] != id.String()+"/iter_2" {
		t.Fatalf("iterations %v, dirs %v", rec.iterations, c.dirs)
	}
	// The failing report and remaining budget must reach the model.
	toolMsg := model.seen[1][len(model.seen[1])-1]
	if toolMsg.Role != llm.RoleTool || toolMsg.ToolCallID != "1" ||
		!strings.Contains(toolMsg.Content, "overlap by 10 mm^3") || !strings.Contains(toolMsg.Content, "3 builds left") {
		t.Fatalf("tool result not fed back: %+v", toolMsg)
	}
}

func TestFinalizeRejectedBeforePassingBuild(t *testing.T) {
	a, _, _ := newAgent()
	model := &scriptedLLM{replies: []llm.Message{
		call("1", toolFinalize, `{"summary": "premature"}`),
		call("2", toolBuild, `{"ok": true}`),
		call("3", toolFinalize, `{"summary": "real"}`),
	}}
	res, err := a.Run(context.Background(), Job{DesignID: uuid.New(), LLM: model, MaxIterations: 3})
	if err != nil || res.Summary != "real" {
		t.Fatalf("res %+v err %v", res, err)
	}
	if got := model.seen[1][len(model.seen[1])-1].Content; !strings.Contains(got, "Cannot finalize") {
		t.Fatalf("expected rejection, got %q", got)
	}
}

func TestRunFailsWhenBudgetExhausted(t *testing.T) {
	a, _, rec := newAgent()
	model := &scriptedLLM{replies: []llm.Message{
		call("1", toolBuild, `{}`),
		call("2", toolBuild, `{}`),
	}}
	res, err := a.Run(context.Background(), Job{DesignID: uuid.New(), LLM: model, MaxIterations: 2})
	if err == nil || res.Passed {
		t.Fatalf("expected failure, got %+v", res)
	}
	if len(rec.iterations) != 2 {
		t.Fatalf("iterations %v", rec.iterations)
	}
}

func TestRunNudgesThenGivesUpWhenModelStopsCallingTools(t *testing.T) {
	a, _, _ := newAgent()
	model := &scriptedLLM{} // always replies with text only
	_, err := a.Run(context.Background(), Job{DesignID: uuid.New(), LLM: model, MaxIterations: 3})
	if err == nil || len(model.seen) != maxNudges+1 {
		t.Fatalf("err %v after %d calls", err, len(model.seen))
	}
}

func TestProviderOutageAfterPassKeepsLastPassingIteration(t *testing.T) {
	a, _, _ := newAgent()
	model := &scriptedLLM{errAt: 4, replies: []llm.Message{
		call("1", toolBuild, `{"ok": true}`),
		call("2", toolBuild, `{"ok": false}`),
		call("3", toolBuild, `{"ok": false}`),
	}}
	res, err := a.Run(context.Background(), Job{DesignID: uuid.New(), LLM: model, MaxIterations: 5})
	if err != nil || !res.Passed || res.FinalIteration != 1 {
		t.Fatalf("res %+v err %v", res, err)
	}
	if !strings.Contains(res.Summary, "503") {
		t.Fatalf("summary should explain the early stop: %q", res.Summary)
	}
}
