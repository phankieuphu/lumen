// Package schema reads table, column, and foreign-key metadata from a
// database so the UI can draw an entity-relationship diagram.
//
// It depends only on the driver interfaces: every engine is read with
// fixed, parameter-free catalog queries over one pinned session.
package schema

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/phankieuphu/lumen/internal/driver"
)

// Limits that keep a huge catalog from being buffered whole.
const (
	DefaultMaxTables  = 1000
	DefaultMaxColumns = 50_000
)

// Column is one table column.
type Column struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	PrimaryKey bool   `json:"primaryKey"`
}

// Table is a table or view.
type Table struct {
	Schema  string   `json:"schema"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind"` // "table" or "view"
	Columns []Column `json:"columns"`
}

// ForeignKey links columns of one table to columns of another, in order.
type ForeignKey struct {
	Name        string   `json:"name"`
	FromSchema  string   `json:"fromSchema"`
	FromTable   string   `json:"fromTable"`
	FromColumns []string `json:"fromColumns"`
	ToSchema    string   `json:"toSchema"`
	ToTable     string   `json:"toTable"`
	ToColumns   []string `json:"toColumns"`
}

// Schema is the relational shape of one database.
type Schema struct {
	Dialect     driver.Dialect `json:"dialect"`
	Tables      []Table        `json:"tables"`
	ForeignKeys []ForeignKey   `json:"foreignKeys"`
	// Truncated is set when a limit stopped the read early.
	Truncated bool `json:"truncated"`
}

// Limits bounds a read. Zero values select the defaults.
type Limits struct {
	MaxTables  int
	MaxColumns int
}

func (l Limits) withDefaults() Limits {
	if l.MaxTables <= 0 {
		l.MaxTables = DefaultMaxTables
	}
	if l.MaxColumns <= 0 {
		l.MaxColumns = DefaultMaxColumns
	}
	return l
}

// ErrUnsupported is returned for a dialect without catalog queries.
var ErrUnsupported = errors.New("schema: dialect not supported")

// Read loads tables, columns, and foreign keys from c.
func Read(ctx context.Context, c driver.Conn, lim Limits) (_ Schema, err error) {
	q, ok := queries[c.Dialect()]
	if !ok {
		return Schema{}, fmt.Errorf("%w: %q", ErrUnsupported, c.Dialect())
	}
	lim = lim.withDefaults()
	s, err := c.Session(ctx)
	if err != nil {
		return Schema{}, fmt.Errorf("schema: %w", err)
	}
	defer func() {
		if cerr := s.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("schema: %w", cerr)
		}
	}()

	out := Schema{Dialect: c.Dialect(), Tables: []Table{}, ForeignKeys: []ForeignKey{}}
	if err := readColumns(ctx, s, q.columns, lim, &out); err != nil {
		return Schema{}, err
	}
	if err := readForeignKeys(ctx, s, q.foreignKeys, &out); err != nil {
		return Schema{}, err
	}
	return out, nil
}

// readColumns expects rows ordered by schema, table, column position:
// (schema, table, kind, column, type, nullable, primary_key).
func readColumns(ctx context.Context, s driver.Session, sql string, lim Limits, out *Schema) (err error) {
	rows, err := s.Query(ctx, sql)
	if err != nil {
		return fmt.Errorf("schema: columns: %w", err)
	}
	defer func() { err = errors.Join(err, closeRows(rows)) }()

	vals := make([]any, len(rows.Columns()))
	if len(vals) != 7 {
		return fmt.Errorf("schema: columns: got %d fields, want 7", len(vals))
	}
	ncols := 0
	for rows.Next() {
		if err := rows.Values(vals); err != nil {
			return fmt.Errorf("schema: columns: %w", err)
		}
		sch, name := str(vals[0]), str(vals[1])
		n := len(out.Tables)
		if n == 0 || out.Tables[n-1].Schema != sch || out.Tables[n-1].Name != name {
			if n == lim.MaxTables {
				out.Truncated = true
				return nil
			}
			out.Tables = append(out.Tables, Table{Schema: sch, Name: name, Kind: kind(str(vals[2]))})
			n++
		}
		if ncols == lim.MaxColumns {
			out.Truncated = true
			return nil
		}
		ncols++
		t := &out.Tables[n-1]
		t.Columns = append(t.Columns, Column{
			Name: str(vals[3]), Type: str(vals[4]), Nullable: truthy(vals[5]), PrimaryKey: truthy(vals[6]),
		})
	}
	return nil
}

// readForeignKeys expects rows ordered by schema, table, constraint, column
// position: (name, from_schema, from_table, from_column, to_schema,
// to_table, to_column). An empty to_column means "the target's primary
// key" (SQLite allows REFERENCES t without columns).
func readForeignKeys(ctx context.Context, s driver.Session, sql string, out *Schema) (err error) {
	rows, err := s.Query(ctx, sql)
	if err != nil {
		return fmt.Errorf("schema: foreign keys: %w", err)
	}
	defer func() { err = errors.Join(err, closeRows(rows)) }()

	vals := make([]any, len(rows.Columns()))
	if len(vals) != 7 {
		return fmt.Errorf("schema: foreign keys: got %d fields, want 7", len(vals))
	}
	tables := make(map[string]*Table, len(out.Tables))
	for i := range out.Tables {
		t := &out.Tables[i]
		tables[key(t.Schema, t.Name)] = t
	}
	var cur *ForeignKey
	flush := func() {
		if cur == nil {
			return
		}
		from, to := tables[key(cur.FromSchema, cur.FromTable)], tables[key(cur.ToSchema, cur.ToTable)]
		// Drop edges to tables cut off by the limits.
		if from != nil && to != nil {
			fillPrimaryKey(cur, to)
			out.ForeignKeys = append(out.ForeignKeys, *cur)
		}
		cur = nil
	}
	for rows.Next() {
		if err := rows.Values(vals); err != nil {
			return fmt.Errorf("schema: foreign keys: %w", err)
		}
		name, fs, ft := str(vals[0]), str(vals[1]), str(vals[2])
		if cur == nil || cur.Name != name || cur.FromSchema != fs || cur.FromTable != ft {
			flush()
			cur = &ForeignKey{Name: name, FromSchema: fs, FromTable: ft, ToSchema: str(vals[4]), ToTable: str(vals[5])}
		}
		cur.FromColumns = append(cur.FromColumns, str(vals[3]))
		cur.ToColumns = append(cur.ToColumns, str(vals[6]))
	}
	flush()
	return nil
}

func fillPrimaryKey(fk *ForeignKey, to *Table) {
	var pk []string
	for _, c := range to.Columns {
		if c.PrimaryKey {
			pk = append(pk, c.Name)
		}
	}
	for i, c := range fk.ToColumns {
		if c == "" && i < len(pk) {
			fk.ToColumns[i] = pk[i]
		}
	}
}

func closeRows(r driver.Rows) error {
	if err := r.Close(); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	return nil
}

func key(schema, table string) string { return schema + "\x00" + table }

func kind(s string) string {
	if strings.EqualFold(s, "view") {
		return "view"
	}
	return "table"
}

// str renders a normalized catalog value as text.
func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

// truthy reads booleans that engines return as bool, integer, or text.
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case int32:
		return x != 0
	case int16:
		return x != 0
	case int8:
		return x != 0
	case uint8:
		return x != 0
	case float64:
		return x != 0
	case string:
		b, err := strconv.ParseBool(x)
		return err == nil && b
	default:
		return false
	}
}
