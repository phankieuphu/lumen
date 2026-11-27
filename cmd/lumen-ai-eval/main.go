// Command lumen-ai-eval scores a configured model against synthetic SQL cases.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"time"

	"github.com/phankieuphu/lumen/internal/ai"
	"github.com/phankieuphu/lumen/internal/ai/prompt"
	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/schema"
	"github.com/phankieuphu/lumen/internal/sqlparse"
)

//go:embed cases.json
var fixture []byte

type evalCase struct {
	Name    string
	Task    prompt.Task
	Dialect driver.Dialect
	Editor  string
	Request string
	Error   string
	Pattern string
}

func main() {
	provider := flag.String("provider", "ollama", "ollama, anthropic, or openai")
	baseURL := flag.String("base-url", "", "provider base URL")
	model := flag.String("model", "", "model to evaluate")
	dryRun := flag.Bool("dry-run", false, "validate fixtures and build prompts without sending requests")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, ai.Config{Provider: ai.ProviderID(*provider), BaseURL: *baseURL, APIKey: os.Getenv("LUMEN_AI_API_KEY")}, *model, *dryRun)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, config ai.Config, model string, dryRun bool) error {
	var cases []evalCase
	if err := json.Unmarshal(fixture, &cases); err != nil {
		return fmt.Errorf("decode fixtures: %w", err)
	}
	checker, err := sqlparse.New()
	if err != nil {
		return err
	}
	defer func() { _ = checker.Close() }()
	var provider ai.Provider
	if !dryRun {
		if model == "" {
			return errors.New("supply -model or use -dry-run")
		}
		provider, err = ai.New(config)
		if err != nil {
			return errors.New("invalid provider configuration")
		}
	}
	pass := 0
	for _, c := range cases {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("evaluation interrupted: %w", err)
		}
		if err := score(ctx, checker, provider, model, c); err != nil {
			fmt.Printf("FAIL %s (%s) %s\n", c.Name, c.Dialect, err)
			continue
		}
		pass++
	}
	if dryRun {
		fmt.Printf("%d/%d fixtures validated without model requests\n", pass, len(cases))
	} else {
		fmt.Printf("%d/%d cases passed\n", pass, len(cases))
	}
	if pass != len(cases) {
		return errors.New("evaluation failed")
	}
	return nil
}

func score(ctx context.Context, checker *sqlparse.Checker, provider ai.Provider, model string, c evalCase) error {
	editor, suffix, cursorMarked := strings.Cut(c.Editor, "|")
	cursor := len(editor)
	editor += suffix
	if c.Task == prompt.FixError && !cursorMarked {
		cursor = 0
	}
	in := prompt.Input{
		Task: c.Task, Dialect: c.Dialect, Editor: editor, Request: c.Request, Error: prompt.ErrorCategory(c.Error), Cursor: cursor,
		Schema: schema.Schema{Dialect: c.Dialect, Tables: []schema.Table{
			{Name: "customers", Columns: []schema.Column{{Name: "id", Type: "integer", PrimaryKey: true}, {Name: "name", Type: "text"}, {Name: "email", Type: "text"}}},
			{Name: "orders", Columns: []schema.Column{{Name: "id", Type: "integer", PrimaryKey: true}, {Name: "customer_id", Type: "integer"}, {Name: "total", Type: "decimal"}, {Name: "status", Type: "text"}}},
		}},
	}
	payload, err := prompt.Build(in)
	if err != nil {
		return err
	}
	pattern, err := regexp.Compile("(?is)" + c.Pattern)
	if err != nil {
		return fmt.Errorf("compile expected SQL pattern: %w", err)
	}
	if provider == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	sql, _, err := prompt.Suggest(ctx, provider, model, payload, in, checker)
	if err != nil {
		return errors.New("suggestion request or SQL validation failed")
	}
	if c.Task == prompt.Inline {
		if sql == "" {
			return errors.New("expected an inline suggestion")
		}
		sql = editor[:cursor] + sql + editor[cursor:]
	}
	if !pattern.MatchString(sql) {
		return errors.New("SQL did not match the expected shape")
	}
	return nil
}
