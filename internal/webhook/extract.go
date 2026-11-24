package webhook

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// A streaming extractor: it walks a JSON document byte by byte, keeps only the values a
// tree of nodes asks for, and skips the rest without decoding or allocating them. The
// body is therefore never held, whatever its size; what is held is what was asked for.
// It runs on a body whose signature is not yet checked, so its result is discarded when
// the signature turns out forged, and what it may hold is capped by maxExtract.
//
// Differences from encoding/json that no host's payload relies on: keys match exactly
// (not case-insensitively), a null array element is skipped, and a duplicated key
// repeats its callback instead of replacing.

const (
	// maxExtract caps the bytes of values held while the signature is unchecked.
	maxExtract = 16 << 20
	// maxValue caps one string value (or key) the extractor decodes, so one forged field cannot
	// take the whole maxExtract budget before the signature is checked. Far above any ref,
	// repository name or path a host sends.
	maxValue = 16 << 10
	// maxDepth bounds nesting so a forged body cannot exhaust the stack.
	maxDepth = 64
)

type kind byte

const (
	kString kind = iota
	kNumber
	kBool
	kNull
)

// node says what to do with one JSON value. A nil *node means skip it.
type node struct {
	kids map[string]*node // object members of interest
	elem *node            // what each array element is, nil to skip them
	// container accepts an object or array; leaf accepts a scalar. A value of the other
	// shape is a type error, as in encoding/json (null is always accepted and ignored).
	container bool
	leaf      func(k kind, v []byte) error
	// onObject runs when an object starts (a non-null value is present).
	onObject func()
}

func obj(kids map[string]*node) *node { return &node{kids: kids, container: true} }

func (n *node) object(f func()) *node { n.onObject = f; return n }

type extractor struct{ held int }

func (x *extractor) hold(n int) error {
	x.held += n
	if x.held > maxExtract {
		return fmt.Errorf("more than %d MiB of values to keep", maxExtract>>20)
	}
	return nil
}

var errType = errors.New("value of the wrong type")

func (x *extractor) str(dst *string) *node {
	return &node{leaf: func(k kind, v []byte) error {
		switch k {
		case kString:
			if err := x.hold(len(v)); err != nil {
				return err
			}
			*dst = string(v)
		case kNull:
		default:
			return errType
		}
		return nil
	}}
}

func (x *extractor) i64(dst *int64) *node { return x.i64At(func() *int64 { return dst }) }

// i64At is i64 into whatever the target currently is, for a value inside an array element.
func (x *extractor) i64At(dst func() *int64) *node {
	return &node{leaf: func(k kind, v []byte) error {
		switch k {
		case kNumber:
			n, err := strconv.ParseInt(string(v), 10, 64)
			if err != nil {
				return errType
			}
			*dst() = n
		case kNull:
		default:
			return errType
		}
		return nil
	}}
}

// repos reads [{"full_name": ..., "id": ...}, ...] into names, one entry per object, and
// the repositories' ids into ids (0 for an object without one) at the same index.
func (x *extractor) repos(dst *[]string, ids *[]int64) *node {
	var cur *string
	var curID *int64
	e := obj(map[string]*node{"id": x.i64At(func() *int64 { return curID }), "full_name": {leaf: func(k kind, v []byte) error {
		switch k {
		case kString:
			if err := x.hold(len(v)); err != nil {
				return err
			}
			*cur = string(v)
		case kNull:
		default:
			return errType
		}
		return nil
	}}})
	e.onObject = func() {
		*dst = append(*dst, "")
		cur = &(*dst)[len(*dst)-1]
		*ids = append(*ids, 0)
		curID = &(*ids)[len(*ids)-1]
	}
	return &node{container: true, elem: e}
}

type scanner struct{ r *bufio.Reader }

// extract walks the document in r, driving root, and requires it to be one complete,
// well-formed value followed by nothing but whitespace.
func extract(r io.Reader, root *node) error {
	s := &scanner{r: bufio.NewReaderSize(r, 32<<10)}
	if err := s.value(root, 0); err != nil {
		return err
	}
	if _, err := s.next(); err != io.EOF {
		if err == nil {
			return errors.New("data after the top-level value")
		}
		return err
	}
	return nil
}

// next returns the next non-space byte, consuming it.
func (s *scanner) next() (byte, error) {
	for {
		c, err := s.r.ReadByte()
		if err != nil {
			return 0, err
		}
		if c != ' ' && c != '\n' && c != '\t' && c != '\r' {
			return c, nil
		}
	}
}

func (s *scanner) need() (byte, error) {
	c, err := s.next()
	if err == io.EOF {
		return 0, io.ErrUnexpectedEOF
	}
	return c, err
}

func (s *scanner) value(n *node, depth int) error {
	c, err := s.need()
	if err != nil {
		return err
	}
	switch c {
	case '{':
		return s.object(n, depth)
	case '[':
		return s.array(n, depth)
	case '"':
		if n == nil || n.leaf == nil {
			if n != nil && n.container {
				return errType
			}
			return s.skipString()
		}
		b, err := s.readString(nil)
		if err != nil {
			return err
		}
		return n.leaf(kString, b)
	}
	lit, err := s.literal(c)
	if err != nil {
		return err
	}
	k := kNumber
	switch lit[0] {
	case 'n':
		k = kNull
	case 't', 'f':
		k = kBool
	}
	if n == nil || k == kNull {
		return nil
	}
	if n.leaf == nil {
		return errType
	}
	return n.leaf(k, lit)
}

func (s *scanner) object(n *node, depth int) error {
	if depth >= maxDepth {
		return errors.New("nested too deeply")
	}
	if n != nil {
		if !n.container {
			return errType
		}
		if n.onObject != nil {
			n.onObject()
		}
	}
	c, err := s.need()
	if err != nil {
		return err
	}
	if c == '}' {
		return nil
	}
	for {
		if c != '"' {
			return errors.New("object key is not a string")
		}
		var child *node
		if n != nil && n.kids != nil {
			key, err := s.readString(nil)
			if err != nil {
				return err
			}
			child = n.kids[string(key)]
		} else if err := s.skipString(); err != nil {
			return err
		}
		if c, err = s.need(); err != nil {
			return err
		}
		if c != ':' {
			return errors.New("missing ':' after object key")
		}
		if err := s.value(child, depth+1); err != nil {
			return err
		}
		if c, err = s.need(); err != nil {
			return err
		}
		switch c {
		case '}':
			return nil
		case ',':
			if c, err = s.need(); err != nil {
				return err
			}
		default:
			return errors.New("expected ',' or '}' in object")
		}
	}
}

func (s *scanner) array(n *node, depth int) error {
	if depth >= maxDepth {
		return errors.New("nested too deeply")
	}
	if n != nil && !n.container {
		return errType
	}
	var elem *node
	if n != nil {
		elem = n.elem
	}
	c, err := s.need()
	if err != nil {
		return err
	}
	if c == ']' {
		return nil
	}
	if err := s.r.UnreadByte(); err != nil {
		return err
	}
	for {
		if err := s.value(elem, depth+1); err != nil {
			return err
		}
		if c, err = s.need(); err != nil {
			return err
		}
		switch c {
		case ']':
			return nil
		case ',':
		default:
			return errors.New("expected ',' or ']' in array")
		}
	}
}

// skipString consumes a string whose opening quote is already read.
func (s *scanner) skipString() error {
	for {
		c, err := s.r.ReadByte()
		if err != nil {
			return unexpected(err)
		}
		switch {
		case c == '"':
			return nil
		case c == '\\':
			if _, err := s.r.ReadByte(); err != nil {
				return unexpected(err)
			}
		case c < 0x20:
			return errors.New("control character in string")
		}
	}
}

// readString reads a string whose opening quote is already read and returns it decoded.
func (s *scanner) readString(dst []byte) ([]byte, error) {
	for {
		if len(dst) > maxValue {
			return nil, fmt.Errorf("a string longer than %d KiB", maxValue>>10)
		}
		c, err := s.r.ReadByte()
		if err != nil {
			return nil, unexpected(err)
		}
		switch {
		case c == '"':
			return dst, nil
		case c == '\\':
			e, err := s.r.ReadByte()
			if err != nil {
				return nil, unexpected(err)
			}
			switch e {
			case '"', '\\', '/':
				dst = append(dst, e)
			case 'b':
				dst = append(dst, '\b')
			case 'f':
				dst = append(dst, '\f')
			case 'n':
				dst = append(dst, '\n')
			case 'r':
				dst = append(dst, '\r')
			case 't':
				dst = append(dst, '\t')
			case 'u':
				r, err := s.hex4()
				if err != nil {
					return nil, err
				}
				if r >= 0xD800 && r < 0xDC00 { // a surrogate pair
					if b, err := s.r.Peek(2); err == nil && b[0] == '\\' && b[1] == 'u' {
						s.r.Discard(2)
						lo, err := s.hex4()
						if err != nil {
							return nil, err
						}
						if lo >= 0xDC00 && lo < 0xE000 {
							r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
						} else {
							dst = append(dst, string(rune(0xFFFD))...)
							r = lo
						}
					}
				}
				if r >= 0xD800 && r < 0xE000 {
					r = 0xFFFD
				}
				dst = append(dst, string(rune(r))...)
			default:
				return nil, errors.New("bad escape in string")
			}
		case c < 0x20:
			return nil, errors.New("control character in string")
		default:
			dst = append(dst, c)
		}
	}
}

func (s *scanner) hex4() (int, error) {
	v := 0
	for i := 0; i < 4; i++ {
		c, err := s.r.ReadByte()
		if err != nil {
			return 0, unexpected(err)
		}
		switch {
		case c >= '0' && c <= '9':
			c -= '0'
		case c >= 'a' && c <= 'f':
			c = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			c = c - 'A' + 10
		default:
			return 0, errors.New("bad \\u escape")
		}
		v = v<<4 | int(c)
	}
	return v, nil
}

// literal reads a number, true, false or null starting with first.
func (s *scanner) literal(first byte) ([]byte, error) {
	b := []byte{first}
	for len(b) < 64 {
		c, err := s.r.ReadByte()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c == '-' || c == '+' || c == '.' || c == 'E') {
			s.r.UnreadByte()
			break
		}
		b = append(b, c)
	}
	switch l := string(b); l {
	case "true", "false", "null":
		return b, nil
	default:
		if validNumber(l) {
			return b, nil
		}
		return nil, fmt.Errorf("invalid literal %q", truncate(l))
	}
}

func truncate(s string) string {
	if len(s) > 20 {
		return s[:20] + "..."
	}
	return s
}

// validNumber reports whether s is a JSON number.
func validNumber(s string) bool {
	i := 0
	if strings.HasPrefix(s, "-") {
		i++
	}
	digits := func() int {
		n := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			n++
		}
		return n
	}
	if i < len(s) && s[i] == '0' {
		i++
	} else if digits() == 0 {
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		if digits() == 0 {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if digits() == 0 {
			return false
		}
	}
	return i == len(s)
}

func unexpected(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

// strsInto is strs for a destination that is only known once its parent object starts.
func (x *extractor) strsInto(dst func() *[]string) *node {
	return &node{container: true, elem: &node{leaf: func(k kind, v []byte) error {
		if k != kString {
			return errType
		}
		if err := x.hold(len(v) + 16); err != nil {
			return err
		}
		d := dst()
		*d = append(*d, string(v))
		return nil
	}}}
}
