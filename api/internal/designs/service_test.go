package designs

import (
	"errors"
	"strings"
	"testing"
)

func TestInterruptedKeepsLastPassingIteration(t *testing.T) {
	out := interrupted(errors.New("context canceled"), 2)
	if !out.Passed || out.FinalIteration != 2 || out.Summary == nil || !strings.Contains(*out.Summary, "context canceled") {
		t.Fatalf("got %+v", out)
	}
}

func TestInterruptedWithoutPassFails(t *testing.T) {
	out := interrupted(nil, 0)
	if out.Passed || out.Error == nil || !strings.Contains(*out.Error, "ended before the run finished") {
		t.Fatalf("got %+v", out)
	}
}
