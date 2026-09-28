// Package cad is the HTTP client for the Python CAD service.
package cad

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Issue struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Parts    []string `json:"parts"`
	Severity string   `json:"severity"`
}

type Artifacts struct {
	GLB  string `json:"glb"`
	STEP string `json:"step"`
}

// Report is the subset of the CAD report the orchestrator reads. Raw holds the
// full report, which is persisted and shown to the model as-is.
type Report struct {
	Passed    bool            `json:"passed"`
	Issues    []Issue         `json:"issues"`
	Metrics   json.RawMessage `json:"metrics,omitempty"`
	Artifacts *Artifacts      `json:"artifacts,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		http:    &http.Client{Timeout: 2 * time.Minute},
	}
}

// Schema returns the AssemblySpec JSON Schema used as the LLM tool schema.
func (c *Client) Schema(ctx context.Context) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, "/schema", nil)
}

// Catalog returns the component catalog and frame conventions.
func (c *Client) Catalog(ctx context.Context) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, "/components", nil)
}

// Build validates a spec and writes STEP/GLB into artifactDir (relative to
// the shared artifacts root).
func (c *Client) Build(ctx context.Context, spec json.RawMessage, artifactDir string) (*Report, error) {
	body, err := json.Marshal(map[string]any{"spec": spec, "artifact_dir": artifactDir})
	if err != nil {
		return nil, err
	}
	raw, err := c.do(ctx, http.MethodPost, "/build", body)
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("decode build report: %w", err)
	}
	r.Raw = raw
	return &r, nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cad %s %s: %w", method, path, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cad %s %s: %s: %s", method, path, res.Status, data)
	}
	return data, nil
}
