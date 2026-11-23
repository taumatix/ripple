package dynamofake

import (
	"fmt"
	"math/big"
	"strings"
	"unicode"
)

// This file is the expression language the store uses: condition and filter expressions
// (comparisons, AND, OR, NOT, parentheses, attribute_exists, attribute_not_exists,
// begins_with, BETWEEN), key conditions, projections and SET/ADD update expressions.
// Anything else is rejected with a ValidationException rather than guessed at, so a store
// change that needs more of the language fails here instead of passing silently.

type av = map[string]any // one DynamoDB AttributeValue in its JSON form: {"S": "x"}

// scope resolves #names and :values for one request and remembers which were used, because
// DynamoDB rejects a request that supplies a name or value no expression refers to.
type scope struct {
	names        map[string]string
	values       map[string]av
	usedN, usedV map[string]bool
}

func newScope(names map[string]string, values map[string]av) *scope {
	return &scope{names: names, values: values, usedN: map[string]bool{}, usedV: map[string]bool{}}
}

func (s *scope) checkUnused() error {
	for k := range s.names {
		if !s.usedN[k] {
			return validation("Value provided in ExpressionAttributeNames unused in expressions: keys: {" + k + "}")
		}
	}
	for k := range s.values {
		if !s.usedV[k] {
			return validation("Value provided in ExpressionAttributeValues unused in expressions: keys: {" + k + "}")
		}
	}
	return nil
}

type token struct{ kind, text string } // kind: id, name, val, op, kw, end

func lex(src string) ([]token, error) {
	var out []token
	rs := []rune(src)
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '(' || c == ')' || c == ',' || c == '=':
			out = append(out, token{"op", string(c)})
			i++
		case c == '<' || c == '>':
			t := string(c)
			if i+1 < len(rs) && (rs[i+1] == '=' || (c == '<' && rs[i+1] == '>')) {
				t += string(rs[i+1])
				i++
			}
			out = append(out, token{"op", t})
			i++
		case c == '#' || c == ':' || c == '_' || unicode.IsLetter(c):
			j := i + 1
			for j < len(rs) && (rs[j] == '_' || rs[j] == '.' || unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j])) {
				j++
			}
			w := string(rs[i:j])
			switch {
			case c == '#':
				out = append(out, token{"name", w})
			case c == ':':
				out = append(out, token{"val", w})
			case isKeyword(w):
				out = append(out, token{"kw", strings.ToUpper(w)})
			default:
				out = append(out, token{"id", w})
			}
			i = j
		default:
			return nil, validation(fmt.Sprintf("Invalid expression: unsupported character %q in %q (the fake supports a subset of the expression language)", c, src))
		}
	}
	return append(out, token{"end", ""}), nil
}

func isKeyword(w string) bool {
	switch strings.ToUpper(w) {
	case "AND", "OR", "NOT", "BETWEEN", "SET", "ADD":
		return true
	}
	return false
}

type operand struct {
	attr  string // set for an attribute path
	val   av
	isVal bool
}

// node is a parsed condition: kind is and, or, not, cmp, between or func.
type node struct {
	kind string
	op   string // comparison operator or function name
	kids []*node
	args []operand
}

type parser struct {
	toks []token
	pos  int
	sc   *scope
	src  string
}

func parseCondition(src string, sc *scope) (*node, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, sc: sc, src: src}
	n, err := p.or()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != "end" {
		return nil, p.fail()
	}
	return n, nil
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) fail() error {
	return validation(fmt.Sprintf("Invalid expression: syntax error near %q in %q (the fake supports a subset of the expression language)", p.peek().text, p.src))
}
func (p *parser) is(kind, text string) bool { t := p.peek(); return t.kind == kind && t.text == text }

func (p *parser) or() (*node, error) {
	l, err := p.and()
	if err != nil {
		return nil, err
	}
	for p.is("kw", "OR") {
		p.next()
		r, err := p.and()
		if err != nil {
			return nil, err
		}
		l = &node{kind: "or", kids: []*node{l, r}}
	}
	return l, nil
}

func (p *parser) and() (*node, error) {
	l, err := p.not()
	if err != nil {
		return nil, err
	}
	for p.is("kw", "AND") {
		p.next()
		r, err := p.not()
		if err != nil {
			return nil, err
		}
		l = &node{kind: "and", kids: []*node{l, r}}
	}
	return l, nil
}

func (p *parser) not() (*node, error) {
	if p.is("kw", "NOT") {
		p.next()
		k, err := p.not()
		if err != nil {
			return nil, err
		}
		return &node{kind: "not", kids: []*node{k}}, nil
	}
	return p.primary()
}

func (p *parser) primary() (*node, error) {
	if p.is("op", "(") {
		p.next()
		n, err := p.or()
		if err != nil {
			return nil, err
		}
		if !p.is("op", ")") {
			return nil, p.fail()
		}
		p.next()
		return n, nil
	}
	if t := p.peek(); t.kind == "id" && p.toks[p.pos+1].text == "(" {
		return p.function()
	}
	l, err := p.operand()
	if err != nil {
		return nil, err
	}
	t := p.next()
	switch {
	case t.kind == "op" && (t.text == "=" || t.text == "<>" || t.text == "<" || t.text == "<=" || t.text == ">" || t.text == ">="):
		r, err := p.operand()
		if err != nil {
			return nil, err
		}
		return &node{kind: "cmp", op: t.text, args: []operand{l, r}}, nil
	case t.kind == "kw" && t.text == "BETWEEN":
		lo, err := p.operand()
		if err != nil {
			return nil, err
		}
		if !p.is("kw", "AND") {
			return nil, p.fail()
		}
		p.next()
		hi, err := p.operand()
		if err != nil {
			return nil, err
		}
		return &node{kind: "between", args: []operand{l, lo, hi}}, nil
	}
	p.pos--
	return nil, p.fail()
}

func (p *parser) function() (*node, error) {
	name := p.next().text
	arity := map[string]int{"attribute_exists": 1, "attribute_not_exists": 1, "begins_with": 2}[name]
	if arity == 0 {
		return nil, validation("Invalid expression: function " + name + " is not supported by the fake (supported: attribute_exists, attribute_not_exists, begins_with)")
	}
	p.next() // (
	var args []operand
	for {
		o, err := p.operand()
		if err != nil {
			return nil, err
		}
		args = append(args, o)
		if p.is("op", ",") {
			p.next()
			continue
		}
		break
	}
	if !p.is("op", ")") || len(args) != arity {
		return nil, p.fail()
	}
	p.next()
	return &node{kind: "func", op: name, args: args}, nil
}

func (p *parser) operand() (operand, error) {
	t := p.next()
	switch t.kind {
	case "id":
		if reserved[strings.ToUpper(t.text)] {
			return operand{}, validation("Invalid expression: Attribute name is a reserved keyword; reserved keyword: " + t.text)
		}
		return operand{attr: t.text}, nil
	case "name":
		n, ok := p.sc.names[t.text]
		if !ok {
			return operand{}, validation("An expression attribute name used in the document path is not defined; attribute name: " + t.text)
		}
		p.sc.usedN[t.text] = true
		return operand{attr: n}, nil
	case "val":
		v, ok := p.sc.values[t.text]
		if !ok {
			return operand{}, validation("An expression attribute value used in expression is not defined; attribute value: " + t.text)
		}
		p.sc.usedV[t.text] = true
		return operand{val: v, isVal: true}, nil
	}
	p.pos--
	return operand{}, p.fail()
}

func (o operand) resolve(it map[string]av) (av, bool) {
	if o.isVal {
		return o.val, true
	}
	v, ok := it[o.attr]
	return v, ok
}

// eval reports whether the item satisfies the condition. A missing attribute fails every
// comparison except <>, as in DynamoDB.
func (n *node) eval(it map[string]av) bool {
	switch n.kind {
	case "and":
		return n.kids[0].eval(it) && n.kids[1].eval(it)
	case "or":
		return n.kids[0].eval(it) || n.kids[1].eval(it)
	case "not":
		return !n.kids[0].eval(it)
	case "func":
		v, ok := n.args[0].resolve(it)
		switch n.op {
		case "attribute_exists":
			return ok
		case "attribute_not_exists":
			return !ok
		default: // begins_with
			p, pok := n.args[1].resolve(it)
			vs, vok := str(v)
			ps, qok := str(p)
			return ok && pok && vok && qok && strings.HasPrefix(vs, ps)
		}
	case "between":
		v, ok1 := n.args[0].resolve(it)
		lo, ok2 := n.args[1].resolve(it)
		hi, ok3 := n.args[2].resolve(it)
		if !ok1 || !ok2 || !ok3 {
			return false
		}
		a, okA := compare(v, lo)
		b, okB := compare(v, hi)
		return okA && okB && a >= 0 && b <= 0
	}
	l, lok := n.args[0].resolve(it)
	r, rok := n.args[1].resolve(it)
	c, comparable := compare(l, r)
	if !lok || !rok || !comparable {
		return n.op == "<>"
	}
	switch n.op {
	case "=":
		return c == 0
	case "<>":
		return c != 0
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	default:
		return c >= 0
	}
}

func str(v av) (string, bool) { s, ok := v["S"].(string); return s, ok }

// compare orders two scalars of the same type: strings and binary by UTF-8 bytes, numbers
// numerically. Values of different or non-scalar types are not comparable.
func compare(a, b av) (int, bool) {
	for _, t := range []string{"S", "B"} {
		x, ok1 := a[t].(string)
		y, ok2 := b[t].(string)
		if ok1 && ok2 {
			return strings.Compare(x, y), true
		}
	}
	x, ok1 := a["N"].(string)
	y, ok2 := b["N"].(string)
	if ok1 && ok2 {
		fx, _, e1 := big.ParseFloat(x, 10, 200, big.ToNearestEven)
		fy, _, e2 := big.ParseFloat(y, 10, 200, big.ToNearestEven)
		if e1 == nil && e2 == nil {
			return fx.Cmp(fy), true
		}
	}
	return 0, false
}

// update is a parsed SET/ADD expression.
type update struct {
	set map[string]av
	add map[string]av
	// order keeps the targets in textual order for stable ReturnValues.
	targets []string
}

func parseUpdate(src string, sc *scope) (*update, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, sc: sc, src: src}
	u := &update{set: map[string]av{}, add: map[string]av{}}
	for p.peek().kind != "end" {
		t := p.next()
		if t.kind != "kw" || (t.text != "SET" && t.text != "ADD") {
			p.pos--
			return nil, validation("Invalid UpdateExpression: only SET and ADD clauses are supported by the fake")
		}
		for {
			path, err := p.operand()
			if err != nil || path.isVal {
				return nil, validation("Invalid UpdateExpression: expected an attribute path in " + src)
			}
			if t.text == "SET" {
				if !p.is("op", "=") {
					return nil, p.fail()
				}
				p.next()
			}
			v, err := p.operand()
			if err != nil || !v.isVal {
				return nil, validation("Invalid UpdateExpression: only literal values (:v) are supported by the fake in " + src)
			}
			if _, dup := u.set[path.attr]; dup {
				return nil, validation("Invalid UpdateExpression: Two document paths overlap with each other")
			}
			if _, dup := u.add[path.attr]; dup {
				return nil, validation("Invalid UpdateExpression: Two document paths overlap with each other")
			}
			if t.text == "SET" {
				u.set[path.attr] = v.val
			} else {
				u.add[path.attr] = v.val
			}
			u.targets = append(u.targets, path.attr)
			if p.is("op", ",") {
				p.next()
				continue
			}
			break
		}
	}
	if len(u.targets) == 0 {
		return nil, validation("Invalid UpdateExpression: empty")
	}
	return u, nil
}

// projection parses "a, #b" into attribute names.
func parseProjection(src string, sc *scope) ([]string, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, sc: sc, src: src}
	var out []string
	for {
		o, err := p.operand()
		if err != nil || o.isVal {
			return nil, validation("Invalid ProjectionExpression: " + src)
		}
		out = append(out, o.attr)
		if p.is("op", ",") {
			p.next()
			continue
		}
		break
	}
	if p.peek().kind != "end" {
		return nil, p.fail()
	}
	return out, nil
}

// reserved holds the DynamoDB reserved words the store's attribute names could plausibly hit
// (the full list has about 570, in the developer guide's "Reserved words in DynamoDB"). A
// word here used bare in an expression is a ValidationException, so the store must alias it
// with an expression attribute name. The list is deliberately a subset: a missing word is a
// gap the DynamoDB Local run still catches, but a word wrongly listed would fail valid code.
var reserved = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`ABORT ADD ALL ALTER AND ANY AS ASC ATTRIBUTE BEGIN BETWEEN BY CASE COLUMN COMMENT COMMIT
		CONNECTION COUNT CREATE CURRENT DATA DATABASE DATE DAY DELETE DESC DOMAIN DROP ELSE END EXISTS EXPIRE FALSE FILE
		FILTER FIRST FOR FROM GROUP HASH HOUR IF IN INDEX INSERT INTO IS ITEM ITEMS KEY KEYS LAST LEVEL LIMIT LIST LOCAL LOCATION
		MAX MIN MINUTE MONTH NAME NOT NULL NUMBER OF ON ONLY OR ORDER OWNER PATH PERCENT PRIMARY RANGE REGION REPLACE RETURN
		ROLE ROW ROWS SCAN SECOND SELECT SET SIZE STATE STATUS STRING TABLE TIME TIMESTAMP TO TOKEN TRUE TYPE UPDATE USER USERS
		VALUE VALUES VIEW WHEN WHERE WITH YEAR ZONE`) {
		m[w] = true
	}
	return m
}()
