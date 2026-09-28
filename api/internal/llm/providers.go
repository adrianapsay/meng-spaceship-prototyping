package llm

import (
	"fmt"
	"os"
	"sort"
)

// Provider is a configured model endpoint.
type Provider struct {
	Name         string `json:"name"`
	DefaultModel string `json:"default_model"`
	baseURL      string
	apiKey       string
}

// Registry holds the providers whose credentials are present in the environment.
type Registry struct {
	providers map[string]Provider
	Default   string
}

type providerEnv struct {
	name, baseURL, keyVar, modelVar, fallbackModel string
	keyOptional                                    bool
}

var knownProviders = []providerEnv{
	{name: "gemini", baseURL: "https://generativelanguage.googleapis.com/v1beta/openai", keyVar: "GEMINI_API_KEY", modelVar: "GEMINI_MODEL", fallbackModel: "gemini-flash-latest"},
	{name: "openai", baseURL: "https://api.openai.com/v1", keyVar: "OPENAI_API_KEY", modelVar: "OPENAI_MODEL"},
	{name: "openrouter", baseURL: "https://openrouter.ai/api/v1", keyVar: "OPENROUTER_API_KEY", modelVar: "OPENROUTER_MODEL"},
	{name: "ollama", baseURL: os.Getenv("OLLAMA_BASE_URL"), modelVar: "OLLAMA_MODEL", keyOptional: true},
}

// RegistryFromEnv registers every provider that has a key (or, for local
// providers, a base URL) and a model.
func RegistryFromEnv() *Registry {
	r := &Registry{providers: map[string]Provider{}}
	for _, p := range knownProviders {
		key := os.Getenv(p.keyVar)
		model := os.Getenv(p.modelVar)
		if model == "" {
			model = p.fallbackModel
		}
		if (key == "" && !p.keyOptional) || p.baseURL == "" || model == "" {
			continue
		}
		r.providers[p.name] = Provider{Name: p.name, DefaultModel: model, baseURL: p.baseURL, apiKey: key}
		if r.Default == "" {
			r.Default = p.name
		}
	}
	if d := os.Getenv("DEFAULT_PROVIDER"); d != "" {
		if _, ok := r.providers[d]; ok {
			r.Default = d
		}
	}
	return r
}

// List returns configured providers sorted by name.
func (r *Registry) List() []Provider {
	out := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Resolve returns a client plus the effective provider and model names.
// Empty arguments fall back to the defaults.
func (r *Registry) Resolve(provider, model string) (Client, string, string, error) {
	if provider == "" {
		if r.Default == "" {
			return nil, "", "", fmt.Errorf("%w: no LLM provider configured (set GEMINI_API_KEY)", ErrUnknownProvider)
		}
		provider = r.Default
	}
	p, ok := r.providers[provider]
	if !ok {
		return nil, "", "", fmt.Errorf("%w: %q", ErrUnknownProvider, provider)
	}
	if model == "" {
		model = p.DefaultModel
	}
	client := &OpenAICompatible{BaseURL: p.baseURL, APIKey: p.apiKey, Model: model, MaxRetries: 6}
	return client, provider, model, nil
}
