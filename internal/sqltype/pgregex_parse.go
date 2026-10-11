package sqltype

import (
	"context"
	"unicode/utf8"
	"unsafe"
)

// PGRegexOptions supplies external operator defaults. Leading PostgreSQL
// directors and options belong to the pattern and override these defaults.
type PGRegexOptions struct{ CaseInsensitive bool }
type PGRegexSpan struct{ Start, End int }

// When captures are requested, Whole and all Captures are byte-boundary
// spans; Captures is borrowed execution scratch until the next Match or Reset.
// Without that request only Matched is meaningful, avoiding unused BOOL work.
type PGRegexMatch struct {
	Matched  bool
	Whole    PGRegexSpan
	Captures []PGRegexSpan
}

func CompilePGRegex(ctx context.Context, pattern string, options PGRegexOptions, collation CollationRef) (*PGRegexProgram, error) {
	return compilePGRegexBudget(ctx, pattern, options, collation, pgRegexMemoryLimit, false)
}
func compilePGRegexBudget(ctx context.Context, pattern string, options PGRegexOptions, collation CollationRef, limit int, validate bool) (*PGRegexProgram, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	work := pgRegexWork{ctx: ctx}
	budget := pgRegexBudget{limit: limit, work: &work}
	if err := budget.reserve(len(pattern)); err != nil {
		return nil, err
	}
	if err := budget.reserve(int(unsafe.Sizeof(PGRegexProgram{}))); err != nil {
		return nil, err
	}
	p := &PGRegexProgram{collation: collation, options: options}
	s := pgRegexParser{program: p, budget: &budget, work: &work}
	lex := pgRegexScanner{ctx: ctx, text: pattern, previous: "empty", caseInsensitive: options.CaseInsensitive}
	if err := lex.prefix(); err != nil {
		return nil, err
	}
	p.options.CaseInsensitive = lex.caseInsensitive
	p.newlineStop, p.newlineAnchor = lex.newlineStop, lex.newlineAnchor
	if err := pgRegexPush(&budget, &s.frames, pgRegexFrame{seq: -1, alt: -1, atom: -1}); err != nil {
		return nil, err
	}
	if err := pgRegexPush(&budget, &s.closed, false); err != nil {
		return nil, err
	}
	for {
		if err := work.step(); err != nil {
			return nil, err
		}
		var tok pgRegexToken
		var err error
		if lex.mode == pgRegexQuote {
			tok.kind = "eof"
			if lex.pos < len(pattern) {
				r, n := utf8.DecodeRuneInString(pattern[lex.pos:])
				lex.pos += n
				tok.kind = "plain"
				tok.code = uint32(r)
			}
		} else {
			tok, err = lex.next()
		}
		if err != nil {
			return nil, err
		}
		f := &s.frames[len(s.frames)-1]
		if tok.kind == "quant" || tok.kind == "bound" {
			if f.atom < 0 || f.quantified || isPGRegexConstraint(s.nodes[f.atom].op) {
				return nil, pgRegexCompileError("quantifier operand invalid")
			}
			lo, hi, fixed := 0, -1, false
			if tok.kind == "bound" {
				if err = lex.bound(); err != nil {
					return nil, err
				}
				tok = lex.boundToken
				lo, hi, fixed = tok.lo, tok.hi, tok.fixed
			} else {
				switch rune(tok.code) {
				case '+':
					lo = 1
				case '?':
					hi = 1
				}
			}
			pref := int8(1)
			if tok.lazy {
				pref = -1
			}
			if fixed {
				pref = s.nodes[f.atom].pref
			}
			n, e := s.repeat(f.atom, lo, hi, pref)
			if e != nil {
				return nil, e
			}
			f.atom = n
			f.quantified = true
			continue
		}
		if err = s.flushAtom(); err != nil {
			return nil, err
		}
		f = &s.frames[len(s.frames)-1]
		switch tok.kind {
		case "eof":
			if len(s.frames) != 1 {
				return nil, pgRegexCompileError("parentheses () not balanced")
			}
			root, e := s.finish()
			if e != nil {
				return nil, e
			}
			p.captures = s.captures
			p.capture = p.captures > 0
			p.backrefs = s.nodes[root].backrefBelow
			p.preference = s.nodes[root].pref
			if validate {
				return p, ctx.Err()
			}
			if err = s.lower(root); err != nil {
				return nil, err
			}
			if err = p.compileClassRanges(&budget); err != nil {
				return nil, err
			}
			budget.used = int(unsafe.Sizeof(PGRegexProgram{})) + len(pattern) + cap(p.code)*int(unsafe.Sizeof(pgRegexInst{})) + cap(p.classes)*int(unsafe.Sizeof(pgRegexClassEntry{})) + cap(p.classRanges)*int(unsafe.Sizeof(pgRegexRange{})) + cap(p.preferences)
			budget.runtime = true
			budget.work = nil
			p.budget = budget
			return p, ctx.Err()
		case "open":
			capID := 0
			if tok.capture && !f.look {
				s.captures++
				capID = s.captures
				lex.captures = s.captures
				if err = pgRegexPush(&budget, &s.closed, false); err != nil {
					return nil, err
				}
			}
			next := pgRegexFrame{seq: -1, alt: -1, atom: -1, capture: capID, look: f.look || tok.look, ownLook: tok.look, lookKind: rune(tok.code)}
			if err = pgRegexPush(&budget, &s.frames, next); err != nil {
				return nil, err
			}
		case "close":
			if len(s.frames) == 1 {
				if lex.mode != pgRegexERE {
					return nil, pgRegexCompileError("parentheses () not balanced")
				}
				n, e := s.node(pgRegexNode{op: pgRegexLiteral, value: ')'})
				if e != nil {
					return nil, e
				}
				f.atom = n
				continue
			}
			n, e := s.finish()
			if e != nil {
				return nil, e
			}
			done := *f
			s.frames = s.frames[:len(s.frames)-1]
			atom := pgRegexNode{op: pgRegexGroup, left: n, pref: s.nodes[n].pref, capture: done.capture != 0, group: done.capture}
			if done.ownLook {
				atom = pgRegexNode{op: pgRegexLook, left: n, value: done.lookKind}
			}
			n, e = s.node(atom)
			if e != nil {
				return nil, e
			}
			if done.capture != 0 {
				s.closed[done.capture] = true
			}
			s.frames[len(s.frames)-1].atom = n
		case "alt":
			if err = s.branch(); err != nil {
				return nil, err
			}
		default:
			atom := pgRegexNode{op: pgRegexLiteral, value: rune(tok.code)}
			switch tok.kind {
			case "any":
				atom.op = pgRegexAny
			case "constraint":
				atom.op = pgRegexConstraint(rune(tok.code))
			case "ref":
				if f.look || tok.code == 0 || int(tok.code) >= len(s.closed) || !s.closed[tok.code] {
					return nil, pgRegexCompileError("invalid backreference number")
				}
				atom.op = pgRegexBackref
			case "class":
				atom, err = s.classToken(tok)
			case "bracket":
				atom, err = s.parseBracket(&lex)
			}
			if err != nil {
				return nil, err
			}
			n, e := s.node(atom)
			if e != nil {
				return nil, e
			}
			f.atom = n
			f.quantified = false
		}
	}
}
func (s *pgRegexParser) flushAtom() error {
	f := &s.frames[len(s.frames)-1]
	if f.atom < 0 {
		return nil
	}
	atom := f.atom
	f.atom = -1
	f.quantified = false
	return s.add(atom)
}
func isPGRegexConstraint(op pgRegexOp) bool {
	return op >= pgRegexBegin && op <= pgRegexNotWordBoundary || op == pgRegexAbsoluteBegin || op == pgRegexAbsoluteEnd || op == pgRegexLook
}
func pgRegexConstraint(r rune) pgRegexOp {
	switch r {
	case '^':
		return pgRegexBegin
	case '$':
		return pgRegexEnd
	case 'A':
		return pgRegexAbsoluteBegin
	case 'Z':
		return pgRegexAbsoluteEnd
	case 'm', '<':
		return pgRegexWordBegin
	case 'M', '>':
		return pgRegexWordEnd
	case 'y':
		return pgRegexWordBoundary
	case 'Y':
		return pgRegexNotWordBoundary
	}
	return pgRegexWordBegin
}
func (s *pgRegexParser) classToken(t pgRegexToken) (pgRegexNode, error) {
	k := pgRegexDigitClass
	if t.code == 's' || t.code == 'S' {
		k = pgRegexSpaceClass
	}
	if t.code == 'w' || t.code == 'W' {
		k = pgRegexWordClass
	}
	lo := len(s.program.classes)
	err := pgRegexPush(s.budget, &s.program.classes, pgRegexClassEntry{class: k, invert: t.code == 'D' || t.code == 'S' || t.code == 'W'})
	return pgRegexNode{op: pgRegexClass, lo: lo, hi: lo + 1}, err
}
func (s *pgRegexParser) bracketEntry(t pgRegexToken) (pgRegexClassEntry, error) {
	e := pgRegexClassEntry{lo: rune(t.code), hi: rune(t.code), literal: t.kind == "plain" || t.kind == "equivalence"}
	if t.kind == "classname" {
		for k, name := range pgRegexClassNames {
			if name == t.name {
				e.class = pgRegexClassKind(k)
				break
			}
		}
	}
	if t.kind == "class" {
		n, err := s.classToken(t)
		if err != nil {
			return e, err
		}
		e = s.program.classes[n.lo]
		s.program.classes = s.program.classes[:n.lo]
	}
	return e, nil
}
func (s *pgRegexParser) parseBracket(lex *pgRegexScanner) (pgRegexNode, error) {
	n := pgRegexNode{op: pgRegexClass, lo: len(s.program.classes)}
	if lex.pos < len(lex.text) && lex.text[lex.pos] == '^' {
		lex.pos++
		n.value = 1
	}
	first := true
	t, err := lex.bracketToken(&first)
	if err != nil {
		return n, err
	}
	for t.kind != "end" {
		if err = s.work.step(); err != nil {
			return n, err
		}
		if t.kind == "range" {
			return n, pgRegexCompileError("invalid character range")
		}
		left, next, e := lex.bracketPart(t, &first)
		if e != nil {
			return n, e
		}
		t = next
		entry, e := s.bracketEntry(left)
		if e != nil {
			return n, e
		}
		if (left.kind == "plain" || left.kind == "collating") && t.kind == "range" {
			t, e = lex.bracketToken(&first)
			if e != nil {
				return n, e
			}
			if t.kind != "plain" && t.kind != "range" && t.kind != "collating" {
				return n, pgRegexCompileError("invalid character range")
			}
			right, next, e := lex.bracketPart(t, &first)
			if e != nil {
				return n, e
			}
			t = next
			if left.code > right.code {
				return n, pgRegexCompileError("invalid character range")
			}
			entry.hi = rune(right.code)
			entry.literal = false
		}
		if err = pgRegexPush(s.budget, &s.program.classes, entry); err != nil {
			return n, err
		}
	}
	n.hi = len(s.program.classes)
	return n, nil
}
