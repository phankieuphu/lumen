// Package prompt builds what lumen sends to a language model: fixed
// instructions for the task, then as much of the schema, the editor, the
// error, and the user's request as fits a token budget, most relevant
// tables first.
//
// Build does no I/O and keeps no state, so the same Input always gives the
// same Context, byte for byte, and the Context lists every section it sends,
// so a preview can show exactly what leaves the computer. Row data never
// reaches it: Input has no field that could carry rows, samples, or
// connection details. String literals from the editor and quoted values in
// error text are masked unless the caller opts in, and password literals are
// always redacted.
//
// Known limit: literals are found with sqlscan's lexer, which assumes
// MySQL's default backslash escapes. On a server running with
// NO_BACKSLASH_ESCAPES, a literal ending in a backslash ends earlier than
// the lexer thinks, so text after it may be masked or sent wrongly.
package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/schema"
	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// Task is what the model is asked to do.
type Task string

// Tasks.
const (
	Inline    Task = "inline"
	TextToSQL Task = "textToSql"
	FixError  Task = "fixError"
)

// Default budgets in estimated input tokens.
const (
	DefaultInlineBudget    = 2000
	DefaultTextToSQLBudget = 8000
	DefaultFixErrorBudget  = 8000
)

// MaxBudget caps Input.Budget, far above any model's context, so budget
// arithmetic cannot overflow.
const MaxBudget = 1 << 24

// Errors Build returns. None carries any of the input text.
var (
	ErrBudget = errors.New("prompt: budget too small for the instructions")
	ErrTask   = errors.New("prompt: unknown task")
	// ErrDialect is returned when neither Input.Dialect nor Schema.Dialect
	// names a known engine: masking literals depends on its quoting rules.
	ErrDialect = errors.New("prompt: unknown dialect")
)

// Privacy chooses what may be sent beyond the schema and SQL structure.
type Privacy struct {
	// KeepLiterals sends editor string literals and quoted values in error
	// text as typed. Zero value (default): replaced with '?'. Password
	// literals are redacted either way.
	KeepLiterals bool
}

// Input is everything Build may use. It deliberately has no field for row
// data, samples, or connection details.
type Input struct {
	Task    Task
	Dialect driver.Dialect // Schema.Dialect if empty
	// Schema is read, never modified.
	Schema schema.Schema
	// Request is what the user typed for the model.
	Request string
	// Error is the database's message for FixError.
	Error string
	// Editor is the editor text; only the statement at Cursor is sent.
	Editor string
	// Cursor is a byte offset into Editor, clamped to a rune boundary. For
	// FixError it should sit inside the statement that failed.
	Cursor int
	// DefaultSchema is the schema unqualified names resolve to: the first
	// schema on PostgreSQL's search_path, MySQL's current database. If
	// empty, public on PostgreSQL and main on SQLite; on MySQL every table
	// is then shown qualified. Tables in other schemas always are.
	DefaultSchema string
	// Budget caps the estimated input tokens; DefaultBudget(Task) if <= 0,
	// and at most MaxBudget.
	Budget  int
	Privacy Privacy
}

// Part names a section of the prompt.
type Part string

// Parts, in the order they are sent.
const (
	PartInstructions    Part = "instructions"
	PartSchema          Part = "schema"
	PartSQL             Part = "sql"
	PartSQLBeforeCursor Part = "sqlBeforeCursor"
	PartSQLAfterCursor  Part = "sqlAfterCursor"
	PartError           Part = "error"
	PartRequest         Part = "request"
)

// Section is one block of the prompt, exactly as sent.
type Section struct {
	Part    Part
	Heading string // a label for the preview; not sent
	Text    string
	Tokens  int // EstimateTokens(Text)
}

// Signal is a set of reasons a table was judged relevant.
type Signal uint8

// Signals.
const (
	InCursorStatement Signal = 1 << iota // named in the statement at the cursor
	InOtherStatement                     // named in another editor statement
	CursorPrefix                         // its name starts with the word at the cursor
	NameMatch                            // a request word is its name
	NamePart                             // a request word is one _-separated part of its name
	ColumnMatch                          // a request word is one of its columns
	InError                              // named in the error text
	Neighbour                            // one foreign key away from a relevant table
)

// Form is how much of a table was sent.
type Form string

// Forms.
const (
	FormFull    Form = "full"
	FormCompact Form = "compact" // key, foreign-key, and matched columns only
)

// TableRef is a table that was sent, in the order sent.
type TableRef struct {
	Schema  string
	Name    string
	Score   int
	Signals Signal
	Form    Form
}

// Reason says why something was left out.
type Reason string

// Reasons.
const (
	ReasonBudget          Reason = "budget"
	ReasonLowRelevance    Reason = "lowRelevance"
	ReasonOtherStatement  Reason = "otherStatement"
	ReasonEditorTooLarge  Reason = "editorTooLarge"
	ReasonSchemaTruncated Reason = "schemaTruncated"
)

// Omission is something left out of the prompt.
type Omission struct {
	Part   Part
	Reason Reason
	// Count is how many tables or statements were left out; for text cut
	// short, the bytes left out. Zero when unknown (schemaTruncated).
	Count int
	// Names lists the first maxOmittedNames tables left out.
	Names []string
}

// Context is a built prompt.
type Context struct {
	Task    Task
	Dialect driver.Dialect
	// SchemaVersion is Schema.Version, for staleness checks. It is not in
	// Key: the version counter restarts with each launch.
	SchemaVersion uint64
	// System and User are what to send: System as the system prompt, User
	// as the one user message.
	System string
	User   string
	// Sections are System, then the parts of User in order.
	Sections []Section
	Tables   []TableRef
	Omitted  []Omission
	// Tokens is the estimate for System plus User; never above Budget.
	Tokens int
	Budget int
	// Key is a hex SHA-256 over the task, dialect, and sent text, for
	// caching answers.
	Key string
}

// Shares of the budget left after the instructions, in percent. The schema
// gets whatever the other parts leave.
const (
	requestShare      = 15
	errorShare        = 15
	editorShare       = 35
	inlineEditorShare = 50
)

// maxOmittedNames caps Omission.Names.
const maxOmittedNames = 20

// keyVersion changes whenever the prompt format does, so cached answers to
// an older format are not reused.
const keyVersion = "lumen-prompt-v1"

// ellipsis marks text cut short.
const ellipsis = "…"

// DefaultBudget returns the budget used when Input.Budget is not set.
func DefaultBudget(t Task) int {
	switch t {
	case Inline:
		return DefaultInlineBudget
	case TextToSQL:
		return DefaultTextToSQLBudget
	case FixError:
		return DefaultFixErrorBudget
	}
	return 0
}

// EstimateTokens returns a cheap, conservative token estimate: one token per
// three bytes, rounded up. Real tokenizers average closer to four bytes for
// English and SQL, so the estimate errs toward sending less.
func EstimateTokens(s string) int { return (len(s) + 2) / 3 }

// Build assembles the prompt for in. It returns ErrTask for an unknown
// task, ErrDialect for an unknown dialect, and ErrBudget when the
// instructions alone exceed the budget.
func Build(in Input) (Context, error) {
	d := in.Dialect
	if d == "" {
		d = in.Schema.Dialect
	}
	system, ok := instructions(in.Task, d)
	if !ok {
		return Context{}, ErrTask
	}
	if d != driver.Postgres && d != driver.MySQL && d != driver.SQLite {
		return Context{}, ErrDialect
	}
	budget := in.Budget
	if budget <= 0 {
		budget = DefaultBudget(in.Task)
	}
	budget = min(budget, MaxBudget)
	sysTokens := EstimateTokens(system)
	if sysTokens > budget {
		return Context{}, ErrBudget
	}

	b := newBuilder(in, d)
	rest := budget - sysTokens

	// Every user section reserves one token beyond its own estimate for the
	// "\n\n" that joins it to the next, so the sum never undercounts.
	request := b.textPart(PartRequest, sqlscan.RedactSecrets(in.Request, sqlscan.Dialect(d)), rest*requestShare/100)
	errText := b.textPart(PartError, b.maskedError(), rest*errorShare/100)
	share := editorShare
	if in.Task == Inline {
		share = inlineEditorShare
	}
	editor := b.editorParts(rest * share / 100)
	used := 0
	for _, s := range request {
		used += s.Tokens + 1
	}
	for _, s := range errText {
		used += s.Tokens + 1
	}
	for _, s := range editor {
		used += s.Tokens + 1
	}
	schemaPart := b.schemaPart(rest - used)

	c := Context{
		Task:          in.Task,
		Dialect:       d,
		SchemaVersion: in.Schema.Version,
		System:        system,
		Budget:        budget,
		Tables:        b.sent,
		Omitted:       b.omitted,
	}
	c.Sections = make([]Section, 0, 6)
	c.Sections = append(c.Sections, Section{Part: PartInstructions, Heading: "Instructions", Text: system, Tokens: sysTokens})
	c.Sections = append(c.Sections, schemaPart...)
	c.Sections = append(c.Sections, editor...)
	c.Sections = append(c.Sections, errText...)
	c.Sections = append(c.Sections, request...)

	n := 0
	for _, s := range c.Sections[1:] {
		n += len(s.Text) + 2
	}
	var u strings.Builder
	u.Grow(n)
	for i, s := range c.Sections[1:] {
		if i > 0 {
			u.WriteString("\n\n")
		}
		u.WriteString(s.Text)
	}
	c.User = u.String()
	c.Tokens = EstimateTokens(c.System) + EstimateTokens(c.User)
	c.Key = key(c)
	return c, nil
}

func key(c Context) string {
	h := sha256.New()
	for _, s := range []string{keyVersion, string(c.Task), string(c.Dialect), c.System, c.User} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// section wraps body in its tags. Before- and after-cursor text is wrapped
// tightly, since whitespace at the cursor matters to a completion.
func section(p Part, body string) Section {
	t := tag(p)
	nl := "\n"
	if p == PartSQLBeforeCursor || p == PartSQLAfterCursor {
		nl = ""
	}
	text := "<" + t + ">" + nl + body + nl + "</" + t + ">"
	return Section{Part: p, Heading: heading(p), Text: text, Tokens: EstimateTokens(text)}
}

// tag and heading name each user part: tag in the text sent, heading in
// the preview.
func tag(p Part) string {
	switch p {
	case PartSchema:
		return "schema"
	case PartSQLBeforeCursor:
		return "sql_before_cursor"
	case PartSQLAfterCursor:
		return "sql_after_cursor"
	case PartError:
		return "error"
	case PartRequest:
		return "request"
	}
	return "sql"
}

func heading(p Part) string {
	switch p {
	case PartSchema:
		return "Schema"
	case PartSQLBeforeCursor:
		return "SQL before the cursor"
	case PartSQLAfterCursor:
		return "SQL after the cursor"
	case PartError:
		return "Error"
	case PartRequest:
		return "Request"
	}
	return "SQL"
}

// overhead returns the bytes section(p, "") adds around a body.
func overhead(p Part) int { return len(section(p, "").Text) }

// bodyBytes returns the most body bytes a section of p may hold within limit
// tokens, counting the one token reserved for the separator.
func bodyBytes(p Part, limit int) int {
	return 3*(limit-1) - overhead(p)
}

// textPart sends s whole if it fits in limit tokens, else its head cut at
// a rune boundary and marked with an ellipsis.
func (b *builder) textPart(p Part, s string, limit int) []Section {
	s = neutral(strings.TrimSpace(s))
	if s == "" {
		return nil
	}
	room := bodyBytes(p, limit)
	if len(s) <= room {
		return []Section{section(p, s)}
	}
	cut := room - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if cut <= 0 {
		b.omit(Omission{Part: p, Reason: ReasonBudget, Count: len(s)})
		return nil
	}
	b.omit(Omission{Part: p, Reason: ReasonBudget, Count: len(s) - cut})
	return []Section{section(p, s[:cut]+ellipsis)}
}

func (b *builder) omit(o Omission) { b.omitted = append(b.omitted, o) }

// closeTag is what neutral rewrites: no section body may close its own or
// another section's tag. The instructions say how it is written instead.
const (
	closeTag        = "</"
	neutralCloseTag = "< /"
)

// neutral rewrites every "</" in s as "< /". Callers apply it before
// measuring, so budgets count the bytes actually sent.
func neutral(s string) string {
	if !strings.Contains(s, closeTag) {
		return s
	}
	return strings.ReplaceAll(s, closeTag, neutralCloseTag)
}
