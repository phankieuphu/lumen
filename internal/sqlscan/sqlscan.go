// Package sqlscan is a small, dialect-aware SQL lexer. It understands enough
// of each dialect's quoting and comment rules to split a script into
// statements and to read leading keywords, without parsing SQL.
//
// It errs toward safety: anything it cannot classify is still returned as a
// token, so callers that look for write keywords see them.
package sqlscan

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Dialect selects quoting and comment rules. Values match driver.Dialect.
type Dialect string

// Dialects.
const (
	Postgres Dialect = "postgres"
	MySQL    Dialect = "mysql"
	SQLite   Dialect = "sqlite"
)

// Kind is a token category.
type Kind uint8

// Token kinds.
const (
	Word        Kind = iota + 1 // keyword or bare identifier
	Number                      // numeric literal
	String                      // quoted string literal, any form
	QuotedIdent                 // "x", `x`, [x]
	Param                       // $1, ?, :name, @name
	Punct                       // any other single character
	Semicolon                   // statement terminator
)

// Token is one lexical token. Text is a slice of the source.
type Token struct {
	Kind Kind
	Text string
	Pos  int
}

// Upper returns the token text upper-cased; useful for keywords.
func (t Token) Upper() string { return strings.ToUpper(t.Text) }

// Is reports whether t is the keyword kw (kw must be upper-case).
func (t Token) Is(kw string) bool {
	return t.Kind == Word && len(t.Text) == len(kw) && strings.EqualFold(t.Text, kw)
}

// Scanner produces tokens from SQL text, skipping whitespace and comments.
type Scanner struct {
	src     string
	pos     int
	dialect Dialect
	// inExecComment is set inside a MySQL /*! ... */ comment, whose body
	// the server executes, so it is scanned as code.
	inExecComment bool
}

// NewScanner returns a scanner over src.
func NewScanner(src string, d Dialect) *Scanner {
	return &Scanner{src: src, dialect: d}
}

// Next returns the next token, or false at end of input.
func (s *Scanner) Next() (Token, bool) {
	for {
		s.skipSpace()
		if s.pos >= len(s.src) {
			return Token{}, false
		}
		if !s.skipComment() {
			break
		}
	}
	start := s.pos
	c := s.src[s.pos]
	switch {
	case c == ';':
		s.pos++
		return Token{Semicolon, ";", start}, true
	case c == '\'':
		s.scanQuoted('\'', s.dialect == MySQL)
		return s.tok(String, start), true
	case c == '"':
		s.scanQuoted('"', s.dialect == MySQL)
		if s.dialect == MySQL {
			return s.tok(String, start), true // ANSI_QUOTES off by default
		}
		return s.tok(QuotedIdent, start), true
	case c == '`' && s.dialect != Postgres:
		s.scanQuoted('`', false)
		return s.tok(QuotedIdent, start), true
	case c == '[' && s.dialect == SQLite:
		s.scanUntil(']')
		return s.tok(QuotedIdent, start), true
	case (c == 'E' || c == 'e') && s.dialect == Postgres && s.peek(1) == '\'':
		s.pos++
		s.scanQuoted('\'', true)
		return s.tok(String, start), true
	case c == '$' && s.dialect == Postgres:
		if s.scanDollarQuote() {
			return s.tok(String, start), true
		}
		if isDigit(s.peek(1)) {
			s.pos++
			s.scanWhile(isDigit)
			return s.tok(Param, start), true
		}
	case c == '?':
		s.pos++
		return s.tok(Param, start), true
	case (c == ':' || c == '@') && isIdentStart(s.peekRune(1)) && s.dialect != Postgres:
		s.pos++
		s.scanWhile(isIdentByte)
		return s.tok(Param, start), true
	case isDigit(c) || (c == '.' && isDigit(s.peek(1))):
		s.scanWhile(func(b byte) bool { return isDigit(b) || b == '.' || b == 'e' || b == 'E' || b == '_' || b == 'x' || b == 'X' })
		return s.tok(Number, start), true
	case isIdentStart(s.peekRune(0)):
		s.scanIdent()
		return s.tok(Word, start), true
	}
	_, size := utf8.DecodeRuneInString(s.src[s.pos:])
	s.pos += size
	return s.tok(Punct, start), true
}

func (s *Scanner) tok(k Kind, start int) Token {
	return Token{Kind: k, Text: s.src[start:s.pos], Pos: start}
}

func (s *Scanner) peek(n int) byte {
	if s.pos+n < len(s.src) {
		return s.src[s.pos+n]
	}
	return 0
}

func (s *Scanner) peekRune(n int) rune {
	if s.pos+n >= len(s.src) {
		return utf8.RuneError
	}
	r, _ := utf8.DecodeRuneInString(s.src[s.pos+n:])
	return r
}

func (s *Scanner) skipSpace() {
	for s.pos < len(s.src) {
		r, size := utf8.DecodeRuneInString(s.src[s.pos:])
		if !unicode.IsSpace(r) {
			return
		}
		s.pos += size
	}
}

// skipComment consumes one comment at pos and reports whether it did.
func (s *Scanner) skipComment() bool {
	c, n := s.src[s.pos], s.peek(1)
	switch {
	case c == '-' && n == '-', c == '#' && s.dialect == MySQL:
		if i := strings.IndexByte(s.src[s.pos:], '\n'); i >= 0 {
			s.pos += i + 1
		} else {
			s.pos = len(s.src)
		}
		return true
	case c == '*' && n == '/' && s.inExecComment:
		s.inExecComment = false
		s.pos += 2
		return true
	case c == '/' && n == '*':
		if s.dialect == MySQL && s.peek(2) == '!' {
			// Executable comment: skip the marker and optional version,
			// then scan the body as code.
			s.pos += 3
			s.scanWhile(isDigit)
			s.inExecComment = true
			return true
		}
		s.skipBlockComment()
		return true
	}
	return false
}

func (s *Scanner) skipBlockComment() {
	s.pos += 2
	depth := 1
	for s.pos < len(s.src) {
		switch {
		case s.src[s.pos] == '*' && s.peek(1) == '/':
			s.pos += 2
			depth--
			if depth == 0 || s.dialect != Postgres {
				return
			}
		case s.src[s.pos] == '/' && s.peek(1) == '*' && s.dialect == Postgres:
			s.pos += 2
			depth++ // PostgreSQL block comments nest
		default:
			s.pos++
		}
	}
}

// scanQuoted consumes a quoted run starting at pos. A doubled quote is an
// escaped quote; with backslash set, \x escapes any character.
func (s *Scanner) scanQuoted(q byte, backslash bool) {
	s.pos++
	for s.pos < len(s.src) {
		c := s.src[s.pos]
		switch {
		case backslash && c == '\\':
			s.pos += 2
		case c == q && s.peek(1) == q:
			s.pos += 2
		case c == q:
			s.pos++
			return
		default:
			s.pos++
		}
	}
	s.pos = min(s.pos, len(s.src))
}

func (s *Scanner) scanUntil(end byte) {
	if i := strings.IndexByte(s.src[s.pos+1:], end); i >= 0 {
		s.pos += i + 2
		return
	}
	s.pos = len(s.src)
}

// scanDollarQuote consumes $tag$...$tag$ and reports whether pos started one.
func (s *Scanner) scanDollarQuote() bool {
	i := s.pos + 1
	for i < len(s.src) && isIdentByte(s.src[i]) && !(i == s.pos+1 && isDigit(s.src[i])) {
		i++
	}
	if i >= len(s.src) || s.src[i] != '$' {
		return false
	}
	tag := s.src[s.pos : i+1]
	if j := strings.Index(s.src[i+1:], tag); j >= 0 {
		s.pos = i + 1 + j + len(tag)
	} else {
		s.pos = len(s.src) // unterminated: the rest is the string
	}
	return true
}

func (s *Scanner) scanIdent() {
	for s.pos < len(s.src) {
		r, size := utf8.DecodeRuneInString(s.src[s.pos:])
		if !(r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return
		}
		s.pos += size
	}
}

func (s *Scanner) scanWhile(f func(byte) bool) {
	for s.pos < len(s.src) && f(s.src[s.pos]) {
		s.pos++
	}
}

func isDigit(b byte) bool     { return b >= '0' && b <= '9' }
func isIdentByte(b byte) bool { return b == '_' || isDigit(b) || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }
func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}
