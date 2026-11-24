package manager

import (
	"fmt"
	"strconv"
	"strings"
)

// A small TOML reader that keeps byte offsets, so a manager can edit a value in place
// and leave comments, layout and key order untouched. It reads the structure of a
// document (tables, array tables, dotted keys, inline tables, arrays, every string
// form) and reports each scalar leaf by its full key path. It does not decode values:
// only a single-line string without escapes is reported with its text, everything else
// is an opaque leaf that is merely known to exist. Arrays are leaves, not descended.
//
// It is lenient where cargo is (an inline table may span lines and carry a trailing
// comma) and strict where a wrong guess would edit the wrong thing (a duplicate key, an
// unterminated string or table header is an error).

// tomlLeaf is one key's value: its full path from the document root and, for a plain
// string, its text. start and end delimit the value in the source, quotes included.
type tomlLeaf struct {
	path       []string
	str        string
	isStr      bool
	start, end int
}

type tomlParser struct {
	src  string
	pos  int
	out  []tomlLeaf
	seen map[string]bool
	arr  int // counts array tables, so two [[x]] headers have distinct paths
}

func tomlScan(src string) ([]tomlLeaf, error) {
	p := &tomlParser{src: src, seen: map[string]bool{}}
	var table []string
	for {
		p.skipBlank()
		if p.pos >= len(p.src) {
			return p.out, nil
		}
		if p.src[p.pos] == '[' {
			var err error
			if table, err = p.header(); err != nil {
				return nil, err
			}
			continue
		}
		key, err := p.key()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if !p.eat('=') {
			return nil, p.errf("expected '=' after key %q", strings.Join(key, "."))
		}
		p.skipSpace()
		if err := p.value(append(append([]string(nil), table...), key...), true); err != nil {
			return nil, err
		}
		if err := p.endOfLine(); err != nil {
			return nil, err
		}
	}
}

func (p *tomlParser) errf(format string, a ...any) error {
	line := 1 + strings.Count(p.src[:min(p.pos, len(p.src))], "\n")
	return fmt.Errorf("toml line %d: %s", line, fmt.Sprintf(format, a...))
}

func (p *tomlParser) eat(c byte) bool {
	if p.pos < len(p.src) && p.src[p.pos] == c {
		p.pos++
		return true
	}
	return false
}

func (p *tomlParser) skipSpace() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
}

// skipBlank skips whitespace, newlines and comments.
func (p *tomlParser) skipBlank() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		case '#':
			p.skipComment()
		default:
			return
		}
	}
}

func (p *tomlParser) skipComment() {
	for p.pos < len(p.src) && p.src[p.pos] != '\n' {
		p.pos++
	}
}

// endOfLine accepts trailing space and a comment, then a newline or the end.
func (p *tomlParser) endOfLine() error {
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] == '#' {
		p.skipComment()
	}
	if p.pos < len(p.src) && p.src[p.pos] == '\r' {
		p.pos++
	}
	if p.pos < len(p.src) {
		if p.src[p.pos] != '\n' {
			return p.errf("unexpected %q after value", p.src[p.pos])
		}
		p.pos++
	}
	return nil
}

func (p *tomlParser) header() ([]string, error) {
	array := strings.HasPrefix(p.src[p.pos:], "[[")
	p.pos++
	if array {
		p.pos++
	}
	p.skipSpace()
	key, err := p.key()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if !p.eat(']') || (array && !p.eat(']')) {
		return nil, p.errf("unterminated table header [%s]", strings.Join(key, "."))
	}
	if err := p.endOfLine(); err != nil {
		return nil, err
	}
	if array {
		p.arr++
		return append(key, "\x00"+strconv.Itoa(p.arr)), nil
	}
	return key, nil
}

// key reads a possibly dotted key: bare, "basic" and 'literal' parts.
func (p *tomlParser) key() ([]string, error) {
	var parts []string
	for {
		p.skipSpace()
		if p.pos >= len(p.src) {
			return nil, p.errf("expected a key")
		}
		switch c := p.src[p.pos]; {
		case c == '"' || c == '\'':
			s, e, simple, err := p.stringAt()
			if err != nil {
				return nil, err
			}
			text := p.src[s+1 : e-1]
			if !simple && c == '"' {
				if u, err := strconv.Unquote(p.src[s:e]); err == nil {
					text = u
				}
			}
			parts = append(parts, text)
		case isBareKey(c):
			s := p.pos
			for p.pos < len(p.src) && isBareKey(p.src[p.pos]) {
				p.pos++
			}
			parts = append(parts, p.src[s:p.pos])
		default:
			return nil, p.errf("unexpected %q, expected a key", c)
		}
		p.skipSpace()
		if !p.eat('.') {
			return parts, nil
		}
	}
}

func isBareKey(c byte) bool {
	return c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// stringAt reads the string starting at p.pos (any of the four forms) and returns its
// span, quotes included. simple says it is one line with no escape, so the text between
// the quotes is the value.
func (p *tomlParser) stringAt() (start, end int, simple bool, err error) {
	start = p.pos
	q := p.src[p.pos]
	if strings.HasPrefix(p.src[p.pos:], strings.Repeat(string(q), 3)) {
		p.pos += 3
		for p.pos < len(p.src) {
			if q == '"' && p.src[p.pos] == '\\' {
				p.pos += 2
				continue
			}
			if strings.HasPrefix(p.src[p.pos:], strings.Repeat(string(q), 3)) {
				p.pos += 3
				for p.pos < len(p.src) && p.src[p.pos] == q { // up to two extra quotes belong to the text
					p.pos++
				}
				return start, p.pos, false, nil
			}
			p.pos++
		}
		return 0, 0, false, p.errf("unterminated multi-line string")
	}
	p.pos++
	simple = true
	for p.pos < len(p.src) {
		switch c := p.src[p.pos]; {
		case c == '\n':
			return 0, 0, false, p.errf("unterminated string")
		case c == q:
			p.pos++
			return start, p.pos, simple, nil
		case c == '\\' && q == '"':
			simple = false
			p.pos += 2
			continue
		}
		p.pos++
	}
	return 0, 0, false, p.errf("unterminated string")
}

// value reads a value at p.pos. With emit, scalar leaves are recorded under path (an
// inline table's members under path+key); inside an array nothing is recorded.
func (p *tomlParser) value(path []string, emit bool) error {
	if p.pos >= len(p.src) {
		return p.errf("expected a value")
	}
	switch c := p.src[p.pos]; c {
	case '"', '\'':
		s, e, simple, err := p.stringAt()
		if err != nil {
			return err
		}
		l := tomlLeaf{start: s, end: e}
		if simple {
			l.isStr, l.str = true, p.src[s+1:e-1]
		}
		return p.add(path, l, emit)
	case '[':
		s := p.pos
		p.pos++
		for {
			p.skipBlank()
			if p.eat(']') {
				break
			}
			if err := p.value(nil, false); err != nil {
				return err
			}
			p.skipBlank()
			if p.eat(',') {
				continue
			}
			if !p.eat(']') {
				return p.errf("expected ',' or ']' in an array")
			}
			break
		}
		return p.add(path, tomlLeaf{start: s, end: p.pos}, emit)
	case '{':
		p.pos++
		p.skipBlank()
		if p.eat('}') {
			return nil
		}
		for {
			key, err := p.key()
			if err != nil {
				return err
			}
			p.skipSpace()
			if !p.eat('=') {
				return p.errf("expected '=' in an inline table")
			}
			p.skipSpace()
			if err := p.value(append(append([]string(nil), path...), key...), emit); err != nil {
				return err
			}
			p.skipBlank()
			if p.eat(',') {
				p.skipBlank()
				if p.eat('}') {
					return nil
				}
				continue
			}
			if !p.eat('}') {
				return p.errf("expected ',' or '}' in an inline table")
			}
			return nil
		}
	default:
		s := p.pos
		for p.pos < len(p.src) && !strings.ContainsRune(" \t\r\n,]}#", rune(p.src[p.pos])) {
			p.pos++
		}
		if p.pos == s {
			return p.errf("unexpected %q, expected a value", c)
		}
		return p.add(path, tomlLeaf{start: s, end: p.pos}, emit)
	}
}

func (p *tomlParser) add(path []string, l tomlLeaf, emit bool) error {
	if !emit {
		return nil
	}
	id := strings.Join(path, "\x01")
	if p.seen[id] {
		return p.errf("duplicate key %q", strings.Join(path, "."))
	}
	p.seen[id] = true
	l.path = path
	p.out = append(p.out, l)
	return nil
}

func tomlUnquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// tomlStripComment cuts a trailing # comment that is outside quotes.
func tomlStripComment(s string) string {
	var quote byte
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return s[:i]
		}
	}
	return s
}
