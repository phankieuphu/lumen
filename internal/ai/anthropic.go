package ai

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// AnthropicModels are offered when the model list cannot be read. The first
// is the default.
var AnthropicModels = []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}

// anthropicProvider calls the Claude API through the official SDK, which
// retries rate limits and overloads itself.
type anthropicProvider struct {
	client anthropic.Client
}

func newAnthropic(cfg Config) *anthropicProvider {
	opts := []option.RequestOption{
		// Only lumen's settings count: ANTHROPIC_BASE_URL or another key in
		// the user's shell must not redirect the saved key or replace it.
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(maxRetries),
		option.WithRequestTimeout(cfg.Timeout),
		option.WithHTTPClient(cfg.HTTPClient),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	return &anthropicProvider{client: anthropic.NewClient(opts...)}
}

func (*anthropicProvider) ID() ProviderID { return Anthropic }
func (*anthropicProvider) Local() bool    { return false }

func (p *anthropicProvider) Complete(ctx context.Context, req Request) (Response, error) {
	params := anthropic.MessageNewParams{
		Model:     req.Model,
		MaxTokens: int64(maxTokens(req)),
		Messages:  make([]anthropic.MessageParam, 0, len(req.Messages)),
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}
	if req.Effort != EffortDefault {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(req.Effort)}
	}
	for _, m := range req.Messages {
		block := anthropic.NewTextBlock(m.Content)
		if m.Role == Assistant {
			params.Messages = append(params.Messages, anthropic.NewAssistantMessage(block))
		} else {
			params.Messages = append(params.Messages, anthropic.NewUserMessage(block))
		}
	}

	start := time.Now()
	msg, err := p.send(ctx, params, req.OnText)
	if err != nil {
		return Response{}, anthropicError(err)
	}
	res := Response{
		Model: msg.Model, Duration: time.Since(start),
		InputTokens: int(msg.Usage.InputTokens), OutputTokens: int(msg.Usage.OutputTokens),
	}
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			res.Text += t.Text
		}
	}
	switch msg.StopReason {
	case anthropic.StopReasonRefusal:
		return res, fmt.Errorf("%w: %s", ErrRefused, msg.StopDetails.Explanation)
	case anthropic.StopReasonMaxTokens:
		return res, ErrTruncated
	}
	return res, nil
}

// send makes the call, streaming when onText is set. Its errors are the
// SDK's own: Complete maps them with anthropicError.
//
//nolint:wrapcheck // wrapped once, by the caller
func (p *anthropicProvider) send(ctx context.Context, params anthropic.MessageNewParams, onText func(string) error) (*anthropic.Message, error) {
	if onText == nil {
		return p.client.Messages.New(ctx, params)
	}
	stream := p.client.Messages.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()
	var msg anthropic.Message
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return nil, err
		}
		if d, ok := ev.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
			if t, ok := d.Delta.AsAny().(anthropic.TextDelta); ok && t.Text != "" {
				if err := onText(t.Text); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (p *anthropicProvider) Models(ctx context.Context) ([]string, error) {
	var out []string
	pager := p.client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{})
	for pager.Next() {
		out = append(out, pager.Current().ID)
	}
	if err := pager.Err(); err != nil {
		return slices.Clone(AnthropicModels), anthropicError(err)
	}
	return out, nil
}

// anthropicError maps an SDK error to the package errors, keeping the API's
// message, which never holds the key.
func anthropicError(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		return fmt.Errorf("%w (HTTP %d): %w", classify(apiErr.StatusCode), apiErr.StatusCode, err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("ai: %w", err)
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}
