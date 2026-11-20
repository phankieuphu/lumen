package ai

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
	"time"
)

// maxErrorBody bounds how much of an error response is kept for the
// message.
const maxErrorBody = 4 << 10

// openAIProvider calls a server with the OpenAI chat completions API:
// POST {base}/chat/completions and GET {base}/models.
type openAIProvider struct {
	id   ProviderID
	base string
	key  string
	http *http.Client
	// timeout bounds one attempt.
	timeout time.Duration
}

func newOpenAI(id ProviderID, base string, cfg Config) *openAIProvider {
	return &openAIProvider{id: id, base: base, key: cfg.APIKey, http: cfg.HTTPClient, timeout: cfg.Timeout}
}

func (p *openAIProvider) ID() ProviderID { return p.id }
func (p *openAIProvider) Local() bool    { return false }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model         string        `json:"model"`
	Messages      []chatMessage `json:"messages"`
	MaxTokens     int           `json:"max_tokens"`
	Stream        bool          `json:"stream,omitempty"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
}

type chatChoice struct {
	Message      chatMessage `json:"message"`
	Delta        chatMessage `json:"delta"`
	FinishReason string      `json:"finish_reason"`
}

type chatResponse struct {
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (p *openAIProvider) Complete(ctx context.Context, req Request) (Response, error) {
	body := chatRequest{Model: req.Model, MaxTokens: maxTokens(req), Stream: req.OnText != nil}
	if req.System != "" {
		body.Messages = append(body.Messages, chatMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, chatMessage{Role: string(m.Role), Content: m.Content})
	}
	if body.Stream {
		body.StreamOptions = &struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("ai: encode the request: %w", err)
	}

	start := time.Now()
	var res Response
	err = p.do(ctx, http.MethodPost, "/chat/completions", payload, func(r io.Reader) error {
		var finish string
		var err error
		if body.Stream {
			res, finish, err = readStream(r, req.OnText)
		} else {
			res, finish, err = readChat(r)
		}
		if err == nil && finish == "length" {
			err = ErrTruncated
		}
		return err
	})
	res.Duration = time.Since(start)
	return res, err
}

func readChat(r io.Reader) (Response, string, error) {
	var cr chatResponse
	if err := json.NewDecoder(r).Decode(&cr); err != nil {
		return Response{}, "", fmt.Errorf("%w: read the answer: %w", ErrUnavailable, err)
	}
	if len(cr.Choices) == 0 {
		return Response{}, "", fmt.Errorf("%w: the answer has no choices", ErrUnavailable)
	}
	res := Response{Text: cr.Choices[0].Message.Content, Model: cr.Model}
	if cr.Usage != nil {
		res.InputTokens, res.OutputTokens = cr.Usage.PromptTokens, cr.Usage.CompletionTokens
	}
	return res, cr.Choices[0].FinishReason, nil
}

// readStream reads server-sent events: "data: {chunk}" lines, ending with
// "data: [DONE]".
func readStream(r io.Reader, onText func(string) error) (Response, string, error) {
	var (
		res    Response
		text   strings.Builder
		finish string
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk chatResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return res, "", fmt.Errorf("%w: read the stream: %w", ErrUnavailable, err)
		}
		if chunk.Model != "" {
			res.Model = chunk.Model
		}
		if chunk.Usage != nil {
			res.InputTokens, res.OutputTokens = chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens
		}
		for _, c := range chunk.Choices {
			if c.FinishReason != "" {
				finish = c.FinishReason
			}
			if c.Delta.Content == "" {
				continue
			}
			text.WriteString(c.Delta.Content)
			if err := onText(c.Delta.Content); err != nil {
				return res, "", err
			}
		}
	}
	res.Text = text.String()
	if err := sc.Err(); err != nil {
		return res, "", fmt.Errorf("%w: read the stream: %w", ErrUnavailable, err)
	}
	return res, finish, nil
}

func (p *openAIProvider) Models(ctx context.Context) ([]string, error) {
	var out []string
	err := p.do(ctx, http.MethodGet, "/models", nil, func(r io.Reader) error {
		var list struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(r).Decode(&list); err != nil {
			return fmt.Errorf("%w: read the model list: %w", ErrUnavailable, err)
		}
		for _, m := range list.Data {
			out = append(out, m.ID)
		}
		return nil
	})
	return out, err
}

// do sends one request to base+path and hands a 2xx body to read. A rate
// limit, an overload, or a dropped connection is retried with backoff,
// unless the answer already started streaming.
func (p *openAIProvider) do(ctx context.Context, method, path string, payload []byte, read func(io.Reader) error) error {
	var err error
	for attempt := 0; ; attempt++ {
		var started bool
		started, err = p.once(ctx, method, path, payload, read)
		if err == nil || started || !retryable(err) || attempt == maxRetries {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(time.Duration(500*(1<<attempt)) * time.Millisecond):
		}
	}
}

func (p *openAIProvider) once(ctx context.Context, method, path string, payload []byte, read func(io.Reader) error) (started bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	hreq, err := http.NewRequestWithContext(ctx, method, p.base+path, body)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrBadRequest, err)
	}
	if payload != nil {
		hreq.Header.Set("Content-Type", "application/json")
	}
	if p.key != "" {
		hreq.Header.Set("Authorization", "Bearer "+p.key)
	}
	resp, err := p.http.Do(hreq)
	if err != nil {
		if ctx.Err() != nil {
			return false, fmt.Errorf("ai: %w", ctx.Err())
		}
		return false, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return false, fmt.Errorf("%w (HTTP %d): %s", classify(resp.StatusCode), resp.StatusCode, errorText(msg))
	}
	return true, read(resp.Body)
}

// errorText is the message of an error body, which is usually
// {"error": {"message": "..."}} or {"error": "..."}.
func errorText(body []byte) string {
	var e struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && len(e.Error) > 0 {
		var obj struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(e.Error, &obj) == nil && obj.Message != "" {
			return obj.Message
		}
		var s string
		if json.Unmarshal(e.Error, &s) == nil && s != "" {
			return s
		}
	}
	return strings.TrimSpace(string(body))
}

// ollamaProvider runs models on this computer. It chats through Ollama's
// OpenAI-compatible endpoint and lists models with its own /api/tags.
type ollamaProvider struct {
	*openAIProvider
	host string
}

func newOllama(host string, cfg Config) *ollamaProvider {
	cfg.APIKey = ""
	return &ollamaProvider{openAIProvider: newOpenAI(Ollama, host+"/v1", cfg), host: host}
}

func (*ollamaProvider) Local() bool { return true }

func (p *ollamaProvider) Models(ctx context.Context) ([]string, error) {
	var out []string
	tags := &openAIProvider{id: Ollama, base: p.host, http: p.http, timeout: p.timeout}
	err := tags.do(ctx, http.MethodGet, "/api/tags", nil, func(r io.Reader) error {
		var list struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.NewDecoder(r).Decode(&list); err != nil {
			return fmt.Errorf("%w: read the model list: %w", ErrUnavailable, err)
		}
		for _, m := range list.Models {
			out = append(out, m.Name)
		}
		return nil
	})
	if errors.Is(err, ErrUnavailable) {
		err = fmt.Errorf("%w: is Ollama running at %s? %w", ErrUnavailable, p.host, err)
	}
	return out, err
}
