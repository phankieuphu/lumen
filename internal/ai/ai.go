// Package ai talks to language models for lumen's AI features through one
// Provider interface, with three providers: Anthropic (the Claude API, via
// the official SDK), any server with the OpenAI chat completions API, and
// Ollama, which runs models on this computer.
//
// The package only moves text. What is sent (schema, SQL, never row data by
// default) is decided by the context builder in internal/ai/prompt, whose
// Context.System and Context.User fill a Request; keys come from the caller,
// who reads them from internal/secret. Nothing here logs prompts, answers,
// or keys.
package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ProviderID names a provider. Values match the UI's ProviderId.
type ProviderID string

// Providers.
const (
	Anthropic ProviderID = "anthropic"
	OpenAI    ProviderID = "openai"
	Ollama    ProviderID = "ollama"
)

// Errors a provider returns, wrapped with the provider's message. Callers
// decide on retries and what to tell the user from these.
var (
	ErrNoKey       = errors.New("ai: no API key is saved for this provider")
	ErrAuth        = errors.New("ai: the provider rejected the API key")
	ErrRateLimit   = errors.New("ai: the provider's rate limit was reached")
	ErrUnavailable = errors.New("ai: the provider is unavailable")
	ErrBadRequest  = errors.New("ai: the provider rejected the request")
	ErrRefused     = errors.New("ai: the model declined to answer")
	ErrTruncated   = errors.New("ai: the answer was cut off at the token limit")
	ErrUnknown     = errors.New("ai: unknown provider")
)

// Role is who wrote a message.
type Role string

// Roles.
const (
	User      Role = "user"
	Assistant Role = "assistant"
)

// Message is one turn of a conversation.
type Message struct {
	Role    Role
	Content string
}

// Effort trades answer quality for speed and cost where the provider
// supports it (Claude's output_config.effort); others ignore it.
type Effort string

// Efforts.
const (
	EffortDefault Effort = ""
	EffortLow     Effort = "low"
	EffortMedium  Effort = "medium"
	EffortHigh    Effort = "high"
)

// DefaultMaxTokens bounds an answer when Request.MaxTokens is zero.
const DefaultMaxTokens = 4096

// Request is one call to a model.
type Request struct {
	Model    string
	System   string
	Messages []Message
	// MaxTokens bounds the answer; DefaultMaxTokens if zero.
	MaxTokens int
	Effort    Effort
	// OnText, when set, streams the answer: it receives each piece of text
	// as it arrives. An error from it stops the call.
	OnText func(text string) error
}

// Response is a model's answer.
type Response struct {
	Text string
	// Model is the model that answered, as the provider reports it.
	Model        string
	InputTokens  int
	OutputTokens int
	Duration     time.Duration
}

// Provider is one way to reach models.
type Provider interface {
	ID() ProviderID
	// Local reports whether requests stay on this computer.
	Local() bool
	// Complete sends req and returns the whole answer.
	Complete(ctx context.Context, req Request) (Response, error)
	// Models lists the models the provider offers.
	Models(ctx context.Context) ([]string, error)
}

// Config selects and configures a provider.
type Config struct {
	Provider ProviderID
	// APIKey is required for Anthropic, optional for an OpenAI-compatible
	// server, and unused by Ollama.
	APIKey string //nolint:gosec // never logged
	// BaseURL overrides the provider's address: required for an
	// OpenAI-compatible server, defaults to http://localhost:11434 for Ollama.
	BaseURL string
	// Timeout bounds one call; DefaultTimeout if zero.
	Timeout time.Duration
	// HTTPClient is for tests; http.DefaultClient's transport if nil.
	HTTPClient *http.Client
}

// Defaults for Config.
const (
	DefaultTimeout   = 2 * time.Minute
	DefaultOllamaURL = "http://localhost:11434"
	// maxRetries is how many times a call is retried after a rate limit,
	// an overload, or a dropped connection.
	maxRetries = 2
)

// New returns the provider cfg selects.
func New(cfg Config) (Provider, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	switch cfg.Provider {
	case Anthropic:
		if cfg.APIKey == "" {
			return nil, ErrNoKey
		}
		return newAnthropic(cfg), nil
	case OpenAI:
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("%w: an OpenAI-compatible provider needs the server's address", ErrBadRequest)
		}
		return newOpenAI(OpenAI, strings.TrimRight(cfg.BaseURL, "/"), cfg), nil
	case Ollama:
		base := cfg.BaseURL
		if base == "" {
			base = DefaultOllamaURL
		}
		return newOllama(strings.TrimRight(base, "/"), cfg), nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknown, cfg.Provider)
}

// classify maps an HTTP status to one of the package errors.
func classify(status int) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ErrAuth
	case status == http.StatusTooManyRequests:
		return ErrRateLimit
	case status >= 500 || status == http.StatusRequestTimeout || status == 529:
		return ErrUnavailable
	default:
		return ErrBadRequest
	}
}

// retryable reports whether a call that failed with err may be tried again.
func retryable(err error) bool {
	return errors.Is(err, ErrRateLimit) || errors.Is(err, ErrUnavailable)
}

// maxTokens is req's answer bound.
func maxTokens(req Request) int {
	if req.MaxTokens > 0 {
		return req.MaxTokens
	}
	return DefaultMaxTokens
}
