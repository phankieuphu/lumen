package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// server answers each request with handler and counts the calls.
func server(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body map[string]any)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
		}
		handler(w, r, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func provider(t *testing.T, cfg Config) Provider {
	t.Helper()
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var ask = Request{Model: "m", System: "You write SQL.", Messages: []Message{{Role: User, Content: "count users"}}}

func TestNew(t *testing.T) {
	if _, err := New(Config{Provider: Anthropic}); !errors.Is(err, ErrNoKey) {
		t.Errorf("anthropic without a key: %v", err)
	}
	if _, err := New(Config{Provider: OpenAI, APIKey: "k"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("openai without an address: %v", err)
	}
	if _, err := New(Config{Provider: "gemini"}); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown: %v", err)
	}
	p := provider(t, Config{Provider: Ollama})
	if p.ID() != Ollama || !p.Local() {
		t.Errorf("ollama: %s local %v", p.ID(), p.Local())
	}
}

func TestOpenAIComplete(t *testing.T) {
	srv, _ := server(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test" {
			http.Error(w, "wrong request", http.StatusBadRequest)
			return
		}
		msgs := body["messages"].([]any)
		first := msgs[0].(map[string]any)
		if len(msgs) != 2 || first["role"] != "system" || body["max_tokens"].(float64) != DefaultMaxTokens || body["stream"] != nil {
			http.Error(w, fmt.Sprint("bad body: ", body), http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"model":"m-1","choices":[{"message":{"role":"assistant","content":"SELECT count(*) FROM users"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":7}}`)
	})
	p := provider(t, Config{Provider: OpenAI, BaseURL: srv.URL + "/v1/", APIKey: "sk-test"})
	res, err := p.Complete(t.Context(), ask)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "SELECT count(*) FROM users" || res.Model != "m-1" || res.InputTokens != 12 || res.OutputTokens != 7 {
		t.Errorf("response: %+v", res)
	}
}

func TestOpenAIStream(t *testing.T) {
	srv, _ := server(t, func(w http.ResponseWriter, _ *http.Request, body map[string]any) {
		if body["stream"] != true {
			http.Error(w, "not streaming", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range []string{
			`{"model":"m-1","choices":[{"delta":{"role":"assistant"}}]}`,
			`{"choices":[{"delta":{"content":"SELECT "}}]}`,
			`{"choices":[{"delta":{"content":"1"},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2}}`,
			`[DONE]`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", line)
		}
	})
	p := provider(t, Config{Provider: OpenAI, BaseURL: srv.URL})
	var pieces []string
	req := ask
	req.OnText = func(s string) error { pieces = append(pieces, s); return nil }
	res, err := p.Complete(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pieces, "|") != "SELECT |1" || res.Text != "SELECT 1" || res.OutputTokens != 2 || res.Model != "m-1" {
		t.Errorf("pieces %q, response %+v", pieces, res)
	}

	req.OnText = func(string) error { return context.Canceled }
	if _, err := p.Complete(t.Context(), req); !errors.Is(err, context.Canceled) {
		t.Errorf("stopped stream: %v", err)
	}
}

func TestOpenAIErrors(t *testing.T) {
	tests := []struct {
		name   string
		status []int // per attempt; the last repeats
		want   error
		calls  int32
	}{
		{"bad key is not retried", []int{401}, ErrAuth, 1},
		{"bad request is not retried", []int{400}, ErrBadRequest, 1},
		{"rate limit then success", []int{429, 200}, nil, 2},
		{"overloaded every time", []int{503}, ErrUnavailable, maxRetries + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var n atomic.Int32
			srv, calls := server(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
				i := min(int(n.Add(1))-1, len(tt.status)-1)
				if s := tt.status[i]; s != 200 {
					w.WriteHeader(s)
					_, _ = io.WriteString(w, `{"error":{"message":"nope"}}`)
					return
				}
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
			})
			p := provider(t, Config{Provider: OpenAI, BaseURL: srv.URL})
			_, err := p.Complete(t.Context(), ask)
			if (tt.want == nil) != (err == nil) || (tt.want != nil && !errors.Is(err, tt.want)) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
			if err != nil && !strings.Contains(err.Error(), "nope") {
				t.Errorf("error lost the server's message: %v", err)
			}
			if calls.Load() != tt.calls {
				t.Errorf("calls = %d, want %d", calls.Load(), tt.calls)
			}
		})
	}
}

func TestOpenAITruncated(t *testing.T) {
	srv, _ := server(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"SELECT a,"},"finish_reason":"length"}]}`)
	})
	p := provider(t, Config{Provider: OpenAI, BaseURL: srv.URL})
	if res, err := p.Complete(t.Context(), ask); !errors.Is(err, ErrTruncated) || res.Text != "SELECT a," {
		t.Errorf("%+v %v", res, err)
	}
}

func TestOllama(t *testing.T) {
	srv, _ := server(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "a key was sent", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[{"name":"qwen3-coder:8b"},{"name":"llama4:16b"}]}`)
		case "/v1/chat/completions":
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"SELECT 1"},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(w, r)
		}
	})
	p := provider(t, Config{Provider: Ollama, BaseURL: srv.URL, APIKey: "ignored"})
	models, err := p.Models(t.Context())
	if err != nil || strings.Join(models, ",") != "qwen3-coder:8b,llama4:16b" {
		t.Errorf("models %v %v", models, err)
	}
	if res, err := p.Complete(t.Context(), ask); err != nil || res.Text != "SELECT 1" {
		t.Errorf("complete %+v %v", res, err)
	}

	down := provider(t, Config{Provider: Ollama, BaseURL: "http://127.0.0.1:1"})
	if _, err := down.Models(t.Context()); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "is Ollama running") {
		t.Errorf("down: %v", err)
	}
}

// anthropicServer imitates POST /v1/messages.
func anthropicServer(t *testing.T, check func(r *http.Request, body map[string]any) string, reply func(w http.ResponseWriter, body map[string]any)) *httptest.Server {
	srv, _ := server(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("Authorization") != "Bearer sk-ant-test" || r.Header.Get("Anthropic-Version") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
			return
		}
		if msg := check(r, body); msg != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"invalid_request_error","message":%q}}`, msg)
			return
		}
		reply(w, body)
	})
	return srv
}

func message(text, stop string) string {
	return fmt.Sprintf(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-5-5",
		"content":[{"type":"text","text":%q}],"stop_reason":%q,"stop_sequence":null,
		"usage":{"input_tokens":20,"output_tokens":6}}`, text, stop)
}

func TestAnthropicComplete(t *testing.T) {
	srv := anthropicServer(t, func(_ *http.Request, body map[string]any) string {
		sys, _ := body["system"].([]any)
		cfg, _ := body["output_config"].(map[string]any)
		switch {
		case body["model"] != "claude-haiku-5-5" || len(sys) != 1:
			return fmt.Sprint("model or system: ", body)
		case cfg["effort"] != "low":
			return fmt.Sprint("effort: ", body["output_config"])
		case len(body["messages"].([]any)) != 3:
			return "messages"
		}
		return ""
	}, func(w http.ResponseWriter, _ map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, message("SELECT count(*) FROM users", "end_turn"))
	})
	p := provider(t, Config{Provider: Anthropic, APIKey: "sk-ant-test", BaseURL: srv.URL})
	req := Request{
		Model: "claude-haiku-5-5", System: "You write SQL.", Effort: EffortLow,
		Messages: []Message{{User, "count users"}, {Assistant, "Which table?"}, {User, "users"}},
	}
	res, err := p.Complete(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "SELECT count(*) FROM users" || res.InputTokens != 20 || res.OutputTokens != 6 || res.Model != "claude-haiku-5-5" {
		t.Errorf("response: %+v", res)
	}

	bad := provider(t, Config{Provider: Anthropic, APIKey: "sk-ant-wrong", BaseURL: srv.URL})
	if _, err := bad.Complete(t.Context(), req); !errors.Is(err, ErrAuth) || strings.Contains(err.Error(), "sk-ant-wrong") {
		t.Errorf("bad key: %v", err)
	}
}

func TestAnthropicIgnoresEnvironment(t *testing.T) {
	elsewhere, calls := server(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) { w.WriteHeader(http.StatusTeapot) })
	t.Setenv("ANTHROPIC_BASE_URL", elsewhere.URL)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-from-env")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Leak: yes")
	srv := anthropicServer(t, func(r *http.Request, _ map[string]any) string {
		if r.Header.Get("X-Leak") != "" {
			return "custom headers from the environment were sent"
		}
		return ""
	}, func(w http.ResponseWriter, _ map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, message("ok", "end_turn"))
	})
	p := provider(t, Config{Provider: Anthropic, APIKey: "sk-ant-test", BaseURL: srv.URL})
	if _, err := p.Complete(t.Context(), Request{Model: "claude-opus-5-5", Messages: ask.Messages}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Error("ANTHROPIC_BASE_URL redirected the request")
	}
}

func TestAnthropicStopReasons(t *testing.T) {
	for stop, want := range map[string]error{"refusal": ErrRefused, "max_tokens": ErrTruncated} {
		srv := anthropicServer(t, func(*http.Request, map[string]any) string { return "" }, func(w http.ResponseWriter, _ map[string]any) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, message("partial", stop))
		})
		p := provider(t, Config{Provider: Anthropic, APIKey: "sk-ant-test", BaseURL: srv.URL})
		if _, err := p.Complete(t.Context(), Request{Model: "claude-opus-5-5", Messages: ask.Messages}); !errors.Is(err, want) {
			t.Errorf("%s: %v", stop, err)
		}
	}
}

func TestAnthropicStream(t *testing.T) {
	srv := anthropicServer(t, func(_ *http.Request, body map[string]any) string {
		if body["stream"] != true {
			return "not streaming"
		}
		return ""
	}, func(w http.ResponseWriter, _ map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`message_start`, `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":9,"output_tokens":1}}}`,
			`content_block_start`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`content_block_delta`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"SELECT "}}`,
			`content_block_delta`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"1"}}`,
			`content_block_stop`, `{"type":"content_block_stop","index":0}`,
			`message_delta`, `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":4}}`,
			`message_stop`, `{"type":"message_stop"}`,
		}
		for i := 0; i < len(events); i += 2 {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", events[i], events[i+1])
		}
	})
	p := provider(t, Config{Provider: Anthropic, APIKey: "sk-ant-test", BaseURL: srv.URL})
	var pieces []string
	res, err := p.Complete(t.Context(), Request{
		Model: "claude-opus-5-5", Messages: ask.Messages,
		OnText: func(s string) error { pieces = append(pieces, s); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pieces, "|") != "SELECT |1" || res.Text != "SELECT 1" || res.OutputTokens != 4 || res.InputTokens != 9 {
		t.Errorf("pieces %q, response %+v", pieces, res)
	}
}
