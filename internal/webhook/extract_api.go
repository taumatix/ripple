package webhook

import "io"

// The streaming extractor for the other hosts' receivers. A receiver describes the
// fields it reads as a tree of Nodes, and Extract fills them while the body streams
// through Guard.ReadVerifiedStream, so a signed body is never held. The result is
// meaningful only once the signature has verified (see ReadVerifiedStream).

// Node says what to do with one JSON value; a nil *Node skips it.
type Node = node

// Extractor holds the byte budget (maxExtract) shared by every value one parse keeps.
// The zero value is ready; use one per body.
type Extractor = extractor

// Extract walks the one JSON document in r, driving root.
func Extract(r io.Reader, root *Node) error { return extract(r, root) }

// Obj is a node for an object whose members of interest are kids; others are skipped.
func Obj(kids map[string]*Node) *Node { return obj(kids) }

// OnObject runs f whenever a non-null object starts at n, so an array element or an
// optional object can note that it is present.
func (n *node) OnObject(f func()) *Node { return n.object(f) }

// Array is a node for an array whose every element is elem (elem nil skips them).
func Array(elem *Node) *Node { return &node{container: true, elem: elem} }

// Str reads a string into dst; null leaves it alone.
func (x *extractor) Str(dst *string) *Node { return x.str(dst) }

// I64 reads an integer into dst; null leaves it alone.
func (x *extractor) I64(dst *int64) *Node { return x.i64(dst) }

// I64Ptr reads an integer into a new int64 that *dst then points to; absent or null
// leaves *dst nil, which is how a field that may be missing is told from a zero.
func (x *extractor) I64Ptr(dst **int64) *Node {
	return &node{leaf: func(k kind, v []byte) error {
		var n int64
		if k == kNull {
			return nil
		}
		if err := x.i64(&n).leaf(k, v); err != nil {
			return err
		}
		*dst = &n
		return nil
	}}
}

// Bool reads a boolean into dst; null leaves it alone.
func (x *extractor) Bool(dst *bool) *Node { return x.boolAt(func() *bool { return dst }) }

// BoolAt is Bool into whatever the target is when the value arrives, for an array element.
func (x *extractor) BoolAt(dst func() *bool) *Node { return x.boolAt(dst) }

func (x *extractor) boolAt(dst func() *bool) *Node {
	return &node{leaf: func(k kind, v []byte) error {
		switch k {
		case kBool:
			*dst() = string(v) == "true"
		case kNull:
		default:
			return errType
		}
		return nil
	}}
}

// StrAt is Str into whatever the target is when the value arrives, for an array element.
func (x *extractor) StrAt(dst func() *string) *Node {
	return &node{leaf: func(k kind, v []byte) error {
		switch k {
		case kString:
			if err := x.hold(len(v)); err != nil {
				return err
			}
			*dst() = string(v)
		case kNull:
		default:
			return errType
		}
		return nil
	}}
}

// I64At is I64 into whatever the target is when the value arrives, for an array element.
func (x *extractor) I64At(dst func() *int64) *Node { return x.i64At(dst) }

// StrsInto reads an array of strings, appending to whatever dst returns when the array
// starts being read, for an array inside an array element.
func (x *extractor) StrsInto(dst func() *[]string) *Node { return x.strsInto(dst) }
