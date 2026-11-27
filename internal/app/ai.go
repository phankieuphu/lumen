package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/phankieuphu/lumen/internal/ai"
	"github.com/phankieuphu/lumen/internal/ai/prompt"
	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/secret"
	"github.com/phankieuphu/lumen/internal/sqlparse"
	"github.com/phankieuphu/lumen/internal/store"
)

// AISettings holds preferences shared by all windows.
type AISettings struct {
	Provider         ai.ProviderID          `json:"provider"`
	Models           map[prompt.Task]string `json:"models"`
	BaseURL          string                 `json:"baseUrl"`
	MaskLiterals     bool                   `json:"maskLiterals"`
	InlineCompletion bool                   `json:"inlineCompletion"`
}

const aiSettingsKey = "ai.settings"

// AIInput identifies the task and the editor snapshot. Cursor uses UTF-8 bytes.
type AIInput struct {
	ConnID  string      `json:"connId"`
	Task    prompt.Task `json:"task"`
	Editor  string      `json:"editor"`
	Cursor  int         `json:"cursor"`
	Request string      `json:"request"`
	Error   string      `json:"error"`
}

// AISettings returns the user's preferences without credentials.
func (a *App) AISettings(ctx context.Context) (AISettings, error) {
	s := AISettings{Provider: ai.Ollama, MaskLiterals: true, Models: map[prompt.Task]string{}}
	raw, err := a.store.Setting(ctx, aiSettingsKey)
	if errors.Is(err, store.ErrNotFound) {
		return s, nil
	}
	if err != nil {
		return AISettings{}, err
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return AISettings{}, errors.New("ai: saved settings are invalid")
	}
	return s, nil
}

func aiEndpoint(s AISettings) (string, error) {
	switch s.Provider {
	case ai.Anthropic:
		return "", nil
	case ai.OpenAI, ai.Ollama:
		if s.BaseURL == "" {
			if s.Provider == ai.Ollama {
				return ai.DefaultOllamaURL, nil
			}
			return "", nil
		}
		u, err := url.Parse(s.BaseURL)
		if err == nil && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
			(u.Scheme == "https" || u.Scheme == "http" && localAIEndpoint(u)) {
			return u.String(), nil
		}
		return "", errors.New("ai: use an HTTPS server URL or a loopback HTTP URL without credentials, query, or fragment")
	}
	return "", ai.ErrUnknown
}

func localAIEndpoint(u *url.URL) bool {
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// SaveAISettings saves preferences. Credentials use a separate keychain call.
func (a *App) SaveAISettings(ctx context.Context, s AISettings) (AISettings, error) {
	s.BaseURL = strings.TrimSpace(s.BaseURL)
	if s.Provider == ai.Anthropic {
		s.BaseURL = ""
	}
	for task, model := range s.Models {
		if prompt.DefaultBudget(task) == 0 || len(model) > 200 || strings.ContainsAny(model, "\r\n") {
			return AISettings{}, errors.New("ai: invalid model settings")
		}
	}
	if _, err := aiEndpoint(s); err != nil {
		return AISettings{}, err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return AISettings{}, errors.New("ai: cannot encode settings")
	}
	return s, a.store.SetSetting(ctx, aiSettingsKey, string(raw))
}

// SetAIKey replaces a remote provider key. An empty value removes it.
func (a *App) SetAIKey(provider ai.ProviderID, key string) error {
	if provider != ai.Anthropic && provider != ai.OpenAI {
		return ai.ErrUnknown
	}
	name := "ai/provider/" + string(provider)
	var err error
	if key == "" {
		err = a.secrets.Delete(name)
	} else {
		err = a.secrets.Set(name, key)
	}
	if err != nil {
		return errors.New("ai: could not update the API key in the keychain")
	}
	return nil
}

// AIPreview returns the exact initial payload text without contacting a provider.
func (a *App) AIPreview(ctx context.Context, input AIInput, settings AISettings) (string, error) {
	c, _, err := a.aiPrompt(ctx, input, settings)
	if err != nil {
		return "", err
	}
	return c.System + "\n\n" + c.User, nil
}

func (a *App) aiPrompt(ctx context.Context, input AIInput, settings AISettings) (prompt.Context, prompt.Input, error) {
	in := prompt.Input{
		Task: input.Task, Dialect: driver.SQLite, Editor: input.Editor, Cursor: input.Cursor,
		Request: input.Request, Error: prompt.ErrorCategory(input.Error), Privacy: prompt.Privacy{KeepLiterals: !settings.MaskLiterals},
	}
	if len(in.Editor) > sqlparse.MaxBytes || len(in.Request) > 8192 || len(input.Error) > 8192 {
		return prompt.Context{}, in, errors.New("ai: request is too large")
	}
	if input.ConnID != "" {
		s, err := a.Schema(ctx, input.ConnID)
		if err != nil {
			return prompt.Context{}, in, err
		}
		in.Schema, in.Dialect = s, s.Dialect
	}
	c, err := prompt.Build(in)
	return c, in, err
}

type suggestionCache struct {
	mu     sync.Mutex
	values map[[32]byte]string
}

func (c *suggestionCache) get(key [32]byte) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.values[key]
	return value, ok
}

func (c *suggestionCache) put(key [32]byte, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.values) >= 128 || c.values == nil {
		c.values = make(map[[32]byte]string)
	}
	c.values[key] = value
}

// AISuggest returns validated SQL for manual review and never executes it.
func (a *App) AISuggest(ctx context.Context, input AIInput) (string, error) {
	ctx, _, done := a.windowScoped(ctx)
	defer done()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	s, err := a.AISettings(ctx)
	if err != nil {
		return "", err
	}
	if input.Task == prompt.Inline && !s.InlineCompletion {
		return "", nil
	}
	model := strings.TrimSpace(s.Models[input.Task])
	if model == "" || input.ConnID == "" {
		return "", errors.New("ai: select a connection and configure a model in AI settings")
	}
	endpoint, err := aiEndpoint(s)
	if err != nil {
		return "", err
	}
	var key string
	if s.Provider != ai.Ollama {
		key, err = a.secrets.Get("ai/provider/" + string(s.Provider))
		if err != nil && !errors.Is(err, secret.ErrNotFound) {
			return "", errors.New("ai: could not read the API key from the keychain")
		}
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	provider, err := ai.New(ai.Config{Provider: s.Provider, APIKey: key, BaseURL: endpoint, HTTPClient: client})
	if err != nil {
		return "", safeAIError(err)
	}
	c, in, err := a.aiPrompt(ctx, input, s)
	if err != nil {
		return "", err
	}
	cacheKey := sha256.Sum256([]byte(strings.Join([]string{c.Key, string(s.Provider), model, endpoint, key, input.ConnID}, "\x00")))
	start := time.Now()
	if answer, ok := a.aiCache.get(cacheKey); ok && prompt.ValidSuggestion(ctx, c, in, a.syntax, answer) {
		return answer, a.logAI(ctx, s.Provider, input.Task, "cached", start, ai.Response{})
	}
	answer, usage, err := prompt.Suggest(ctx, provider, model, c, in, a.syntax)
	status := "ok"
	if err != nil {
		status = "error"
		if ctx.Err() != nil {
			status = "cancelled"
		}
	}
	logErr := a.logAI(ctx, s.Provider, input.Task, status, start, usage)
	if err != nil {
		return "", safeAIError(err)
	}
	if logErr != nil {
		return "", logErr
	}
	a.aiCache.put(cacheKey, answer)
	return answer, nil
}

func (a *App) logAI(ctx context.Context, provider ai.ProviderID, task prompt.Task, status string, start time.Time, usage ai.Response) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	_, err := a.store.DB().ExecContext(ctx, `INSERT INTO ai_requests
		(created_at, provider, task, status, duration_ms, input_tokens, output_tokens) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		store.Now(), provider, task, status, time.Since(start).Milliseconds(), usage.InputTokens, usage.OutputTokens)
	if err != nil {
		return errors.New("ai: could not record request metadata")
	}
	return nil
}

func safeAIError(err error) error {
	for _, safe := range []error{
		context.Canceled, context.DeadlineExceeded, ai.ErrNoKey, ai.ErrAuth,
		ai.ErrRateLimit, ai.ErrUnavailable, ai.ErrBadRequest, ai.ErrRefused, ai.ErrTruncated, prompt.ErrInvalidSQL, prompt.ErrUnsupportedSQL,
	} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	return errors.New("ai: suggestion request failed")
}
