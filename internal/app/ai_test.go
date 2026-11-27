package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phankieuphu/lumen/internal/ai"
	"github.com/phankieuphu/lumen/internal/ai/prompt"
)

func TestAISuggestionPrivacyAndCache(t *testing.T) {
	a := openTestApp(t)
	ctx := t.Context()
	c, err := a.CreateConnection(ctx, ConnectionInput{
		Name: "local", Dialect: "sqlite", Database: filepath.Join(t.TempDir(), "private.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, sql := range []string{
		"CREATE TABLE customers (id INTEGER, name TEXT)",
		"INSERT INTO customers VALUES (1, 'private-row')",
	} {
		if _, err := a.RunQuery(ctx, RunInput{RequestID: fmt.Sprintf("setup-%d", i), ConnID: c.ID, SQL: sql}); err != nil {
			t.Fatal(err)
		}
	}
	const (
		apiKey = "test-provider-key"
		answer = "SELECT name FROM customers;"
	)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct{ Messages []ai.Message }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		var user string
		for _, msg := range body.Messages {
			if msg.Role == ai.User {
				user = msg.Content
			}
		}
		if !strings.Contains(user, "customers") || !strings.Contains(user, "'?'") ||
			strings.Contains(user, "private-row") || strings.Contains(user, "private-literal") || strings.Contains(user, apiKey) {
			t.Error("provider received the wrong context")
		}
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			t.Error("missing keychain API key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}]}`, "```sql\n"+answer+"\n```")
	}))
	t.Cleanup(server.Close)
	s, err := a.AISettings(ctx)
	if err != nil || !s.MaskLiterals || s.InlineCompletion {
		t.Fatalf("unexpected default settings: %+v, %v", s, err)
	}
	s.Provider, s.BaseURL = ai.OpenAI, server.URL
	s.Models[prompt.FixError] = "test-model"
	if _, err := a.SaveAISettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := a.SetAIKey(ai.OpenAI, apiKey); err != nil {
		t.Fatal(err)
	}
	in := AIInput{
		ConnID: c.ID, Task: prompt.FixError, Editor: "SELECT nme FROM customers WHERE name = 'private-literal'",
		Error: "no such column: private-row",
	}
	preview, err := a.AIPreview(ctx, in, s)
	if err != nil || strings.Contains(preview, "private") || !strings.Contains(preview, "unknown column") || calls.Load() != 0 {
		t.Fatalf("preview did not stay private: %s, %v", preview, err)
	}
	for range 2 {
		if got, err := a.AISuggest(ctx, in); err != nil || got != answer {
			t.Fatalf("suggestion = %q, %v", got, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("provider called %d times", calls.Load())
	}
	var total, cached int
	if err := a.store.DB().QueryRowContext(ctx, "SELECT COUNT(*), SUM(status = 'cached') FROM ai_requests").Scan(&total, &cached); err != nil {
		t.Fatal(err)
	}
	if total != 2 || cached != 1 {
		t.Fatalf("request log totals = %d/%d", cached, total)
	}
	raw, err := a.store.Setting(ctx, aiSettingsKey)
	if err != nil || strings.Contains(raw, apiKey) {
		t.Fatal("settings written to SQLite must exclude the API key")
	}
}

func TestAISuggestionCancellation(t *testing.T) {
	a := openTestApp(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, err := a.CreateConnection(ctx, ConnectionInput{
		Name: "local", Dialect: "sqlite", Database: filepath.Join(t.TempDir(), "cancel.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Acknowledge the request headers so the transport can propagate
		// cancellation even while the result body is still pending.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		started <- struct{}{}
		select {
		case <-r.Context().Done():
			stopped <- struct{}{}
		case <-t.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	s := AISettings{
		Provider: ai.OpenAI, BaseURL: server.URL, MaskLiterals: true, InlineCompletion: true,
		Models: map[prompt.Task]string{prompt.Inline: "test-model"},
	}
	if _, err := a.SaveAISettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := a.AISuggest(ctx, AIInput{ConnID: c.ID, Task: prompt.Inline, Editor: "SELECT", Cursor: 6})
		done <- err
	}()
	select {
	case <-started:
		cancel()
	case <-ctx.Done():
		t.Error("provider request did not start")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("suggestion error = %v, want canceled", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("provider request survived cancellation")
	}
	var status string
	if err := a.store.DB().QueryRowContext(t.Context(), "SELECT status FROM ai_requests").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" {
		t.Fatalf("request status = %q", status)
	}
}
