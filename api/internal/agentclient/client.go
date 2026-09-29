// Package agentclient is the HTTP client for the Python agent service.
package agentclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrRejected means the agent service refused the request (e.g. an unknown provider).
var ErrRejected = errors.New("agent rejected request")

type Provider struct {
	Name         string `json:"name"`
	DefaultModel string `json:"default_model"`
}

type Providers struct {
	Default   string     `json:"default"`
	Providers []Provider `json:"providers"`
}

type RunRequest struct {
	DesignID      string `json:"design_id"`
	Prompt        string `json:"prompt"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	MaxIterations int    `json:"max_iterations"`
}

// Event is one line of the run stream. The last event is "run.finished".
type Event struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	// No client timeout: runs stream for minutes. Callers bound them with a context.
	return &Client{baseURL: strings.TrimSuffix(baseURL, "/"), http: &http.Client{}}
}

func (c *Client) Providers(ctx context.Context) (Providers, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/providers", nil)
	if err != nil {
		return Providers{}, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return Providers{}, fmt.Errorf("agent providers: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return Providers{}, fmt.Errorf("agent providers: %s: %s", res.Status, body)
	}
	var p Providers
	return p, json.NewDecoder(res.Body).Decode(&p)
}

// Run starts a design run and calls fn for each event until the stream ends.
// Cancelling ctx disconnects, which cancels the run on the agent side.
func (c *Client) Run(ctx context.Context, run RunRequest, fn func(Event) error) error {
	body, err := json.Marshal(run)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/runs", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("agent run: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(res.Body)
		if res.StatusCode == http.StatusBadRequest || res.StatusCode == http.StatusUnprocessableEntity {
			return fmt.Errorf("%w: %s", ErrRejected, detail)
		}
		return fmt.Errorf("agent run: %s: %s", res.Status, detail)
	}

	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 16<<20) // one line carries a full spec and report
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return fmt.Errorf("agent run: bad event: %w", err)
		}
		if err := fn(ev); err != nil {
			return err
		}
	}
	return sc.Err()
}
