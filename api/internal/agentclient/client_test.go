package agentclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunStreamsEventsInOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req RunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DesignID != "d1" {
			t.Errorf("bad request %+v %v", req, err)
		}
		io.WriteString(w, `{"event":"iteration.started","data":{"n":1}}`+"\n\n")
		io.WriteString(w, `{"event":"run.finished","data":{"passed":true}}`+"\n")
	}))
	defer srv.Close()

	var got []string
	err := New(srv.URL).Run(context.Background(), RunRequest{DesignID: "d1"}, func(ev Event) error {
		got = append(got, ev.Event)
		return nil
	})
	if err != nil || len(got) != 2 || got[0] != "iteration.started" || got[1] != "run.finished" {
		t.Fatalf("events %v err %v", got, err)
	}
}

func TestRunRejectedRequestIsErrRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"unknown provider"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	err := New(srv.URL).Run(context.Background(), RunRequest{}, func(Event) error { return nil })
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("got %v", err)
	}
}

func TestProvidersDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"default":"gemini","providers":[{"name":"gemini","default_model":"m"}]}`)
	}))
	defer srv.Close()

	p, err := New(srv.URL).Providers(context.Background())
	if err != nil || p.Default != "gemini" || p.Providers[0].DefaultModel != "m" {
		t.Fatalf("got %+v %v", p, err)
	}
}
