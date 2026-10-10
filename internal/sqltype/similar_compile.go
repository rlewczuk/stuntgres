package sqltype

import (
	"context"
	"unicode/utf8"
	"unsafe"
)

const similarIntBytes = int(unsafe.Sizeof(int(0)))

type similarBudget struct {
	used, limit int
	runtime     bool
	work        *similarWork
}

func (b *similarBudget) reserve(n int) error {
	if n < 0 || n > b.limit-b.used {
		if b.runtime {
			return similarRuntimeError()
		}
		return similarCompileError("regular expression is too complex")
	}
	b.used += n
	return nil
}

// Account both old and replacement backing arrays while copying. Growth and
// long copies are cancellation-aware, and no append may implicitly allocate.
func similarGrow[T any](b *similarBudget, s []T, need int) ([]T, error) {
	if need <= cap(s) {
		return s, nil
	}
	n := max(8, cap(s)*2)
	if n < need {
		n = need
	}
	width := int(unsafe.Sizeof(*new(T)))
	if n > b.limit/width {
		return nil, b.reserve(b.limit + 1)
	}
	if err := b.reserve(n * width); err != nil {
		return nil, err
	}
	next := make([]T, len(s), n)
	for i := range s {
		if err := b.work.step(); err != nil {
			b.used -= n * width
			return nil, err
		}
		next[i] = s[i]
	}
	b.used -= cap(s) * width
	return next, nil
}
func similarPush[T any](b *similarBudget, s *[]T, v T) error {
	next, err := similarGrow(b, *s, len(*s)+1)
	if err != nil {
		return err
	}
	*s = append(next, v)
	return nil
}

type similarOp uint8

const (
	similarEmpty similarOp = iota
	similarLiteral
	similarAny
	similarClass
	similarBegin
	similarEnd
	similarWordBegin
	similarWordEnd
	similarWordBoundary
	similarNotWordBoundary
	similarBackref
	similarConcat
	similarAlt
	similarRepeat
	similarGroup
	similarSplit
	similarTag
	similarReset
	similarProgressBegin
	similarProgressEnd
	similarAccept
)

type similarNode struct {
	op                                         similarOp
	left, right                                int
	lo, hi                                     int
	value                                      rune
	pref                                       int8
	capture, captureBelow, backrefBelow, plain bool
	rank, rankLo, rankHi, progress             int
}
type similarInst struct {
	op     similarOp
	x, y   int
	value  rune
	lo, hi int
}
type similarFrame struct {
	seq, alt int
	capture  bool
}

// SimilarProgram has immutable instructions and execution-local reusable scratch.
// An owning execution must not call Match concurrently. No input is retained.
type SimilarProgram struct {
	code                                    []similarInst
	classes                                 []similarClassEntry
	start                                   int
	collation                               CollationRef
	capture                                 bool
	preferences                             []int8
	progress                                int
	budget                                  similarBudget
	matchWork                               similarWork
	current, next, stack                    []int
	seen                                    []uint32
	generation                              uint32
	tagged                                  []int
	table                                   []int
	histories                               []similarHistory
	historyTable, historyLeft, historyRight []int
}

// HasCapture reports immutable compiler metadata. A successful Match with
// negative offsets means a nonparticipating capture when this returns true,
// or the whole source when it returns false.
func (p *SimilarProgram) HasCapture() bool { return p.capture }

type similarParser struct {
	text     string
	at       int
	nodes    []similarNode
	frames   []similarFrame
	program  *SimilarProgram
	budget   *similarBudget
	work     *similarWork
	captures int
	closed   bool
}

func (s *similarParser) node(n similarNode) (int, error) {
	n.rank = -1
	n.rankLo = -1
	n.progress = -1
	n.captureBelow = n.capture
	n.backrefBelow = n.op == similarBackref
	if !n.plain {
		if n.op == similarGroup || n.op == similarRepeat || n.op == similarConcat || n.op == similarAlt {
			n.captureBelow = n.captureBelow || s.nodes[n.left].captureBelow
			n.backrefBelow = n.backrefBelow || s.nodes[n.left].backrefBelow
		}
		if n.op == similarConcat || n.op == similarAlt {
			n.captureBelow = n.captureBelow || s.nodes[n.right].captureBelow
			n.backrefBelow = n.backrefBelow || s.nodes[n.right].backrefBelow
		}
	}
	i := len(s.nodes)
	err := similarPush(s.budget, &s.nodes, n)
	return i, err
}

func (s *similarParser) repeat(atom, lo, hi int, pref int8) (int, error) {
	body := s.nodes[atom]
	if lo > 0 && body.captureBelow && !body.backrefBelow {
		// Release regcomp's capture-only path is x{m-1,n-1}x. The plain
		// prefix has priority over the last (capturing) operand, including
		// when a fixed count inherits a nongreedy operand preference.
		end := hi
		if end >= 0 {
			end--
		}
		prefix, err := s.node(similarNode{op: similarRepeat, left: atom, lo: lo - 1, hi: end, pref: pref, plain: true})
		if err != nil {
			return 0, err
		}
		joined, err := s.node(similarNode{op: similarConcat, left: prefix, right: atom, pref: pref})
		if err != nil {
			return 0, err
		}
		return s.node(similarNode{op: similarGroup, left: joined, pref: pref})
	}
	return s.node(similarNode{op: similarRepeat, left: atom, lo: lo, hi: hi, pref: pref})
}
func (s *similarParser) combine(op similarOp, a, b int) (int, error) {
	pref := s.nodes[a].pref
	if op == similarAlt {
		pref = 1
	} else if pref == 0 {
		pref = s.nodes[b].pref
	}
	return s.node(similarNode{op: op, left: a, right: b, pref: pref})
}
func (s *similarParser) add(n int) error {
	f := &s.frames[len(s.frames)-1]
	if f.seq < 0 {
		f.seq = n
		return nil
	}
	v, err := s.combine(similarConcat, f.seq, n)
	if err != nil {
		return err
	}
	f.seq = v
	return nil
}
func (s *similarParser) branch() error {
	f := &s.frames[len(s.frames)-1]
	seq := f.seq
	if seq < 0 {
		var err error
		seq, err = s.node(similarNode{op: similarEmpty})
		if err != nil {
			return err
		}
	}
	if f.alt < 0 {
		f.alt = seq
	} else {
		v, err := s.combine(similarAlt, f.alt, seq)
		if err != nil {
			return err
		}
		f.alt = v
	}
	f.seq = -1
	return nil
}
func (s *similarParser) finish() (int, error) {
	f := s.frames[len(s.frames)-1]
	seq := f.seq
	if seq < 0 {
		var err error
		seq, err = s.node(similarNode{op: similarEmpty})
		if err != nil {
			return 0, err
		}
	}
	if f.alt >= 0 {
		return s.combine(similarAlt, f.alt, seq)
	}
	return seq, nil
}
func (s *similarParser) read() rune {
	r, n := utf8.DecodeRuneInString(s.text[s.at:])
	s.at += n
	return r
}
func (s *similarParser) peek() rune {
	if s.at == len(s.text) {
		return -1
	}
	r, _ := utf8.DecodeRuneInString(s.text[s.at:])
	return r
}

func CompileSimilar(ctx context.Context, pattern string, collation CollationRef) (*SimilarProgram, error) {
	return compileSimilarBudget(ctx, pattern, collation, similarMemoryLimit)
}
func compileSimilarBudget(ctx context.Context, pattern string, collation CollationRef, limit int) (*SimilarProgram, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	work := similarWork{ctx: ctx}
	budget := similarBudget{limit: limit, work: &work}
	// Canonical translation is new execution-owned storage, not the caller's
	// already-owned SQL pattern. Its bytes share the IR/scratch allowance.
	if err := budget.reserve(len(pattern)); err != nil {
		return nil, err
	}
	if err := budget.reserve(int(unsafe.Sizeof(SimilarProgram{}))); err != nil {
		return nil, err
	}
	p := &SimilarProgram{collation: collation}
	s := similarParser{text: pattern, program: p, budget: &budget, work: &work}
	if err := similarPush(&budget, &s.frames, similarFrame{seq: -1, alt: -1}); err != nil {
		return nil, err
	}
	for s.at < len(pattern) {
		if err := work.step(); err != nil {
			return nil, err
		}
		c := s.read()
		if c == '(' {
			capture := true
			if s.peek() == '?' {
				s.at++
				if s.peek() != ':' {
					return nil, similarCompileError("quantifier operand invalid")
				}
				s.at++
				capture = false
			}
			if capture {
				s.captures++
				if s.captures > 1 {
					return nil, similarCompileError("invalid backreference number")
				}
				p.capture = true
			}
			if err := similarPush(&budget, &s.frames, similarFrame{seq: -1, alt: -1, capture: capture}); err != nil {
				return nil, err
			}
			continue
		}
		if c == '|' {
			if err := s.branch(); err != nil {
				return nil, err
			}
			continue
		}
		var n int
		var err error
		if c == ')' {
			if len(s.frames) == 1 {
				return nil, similarCompileError("parentheses () not balanced")
			}
			n, err = s.finish()
			if err != nil {
				return nil, err
			}
			f := s.frames[len(s.frames)-1]
			s.frames = s.frames[:len(s.frames)-1]
			n, err = s.node(similarNode{op: similarGroup, left: n, pref: s.nodes[n].pref, capture: f.capture})
			if f.capture {
				s.closed = true
			}
		} else {
			atom := similarNode{op: similarLiteral, value: c}
			switch c {
			case '*', '+', '?':
				return nil, similarCompileError("quantifier operand invalid")
			case '{':
				if s.peek() >= '0' && s.peek() <= '9' {
					return nil, similarCompileError("quantifier operand invalid")
				}
			case '[':
				atom, err = s.bracket()
			case '\\':
				atom, err = s.escape(false)
			case '.':
				atom.op = similarAny
			case '^':
				atom.op = similarBegin
			case '$':
				atom.op = similarEnd
			}
			if err != nil {
				return nil, err
			}
			n, err = s.node(atom)
		}
		if err != nil {
			return nil, err
		}
		if s.at < len(pattern) {
			q := s.peek()
			if q == '*' || q == '+' || q == '?' || q == '{' && s.at+1 < len(pattern) && pattern[s.at+1] >= '0' && pattern[s.at+1] <= '9' {
				if s.nodes[n].op >= similarBegin && s.nodes[n].op <= similarNotWordBoundary {
					return nil, similarCompileError("quantifier operand invalid")
				}
				lo, hi, fixed := 0, -1, false
				s.at++
				switch q {
				case '+':
					lo = 1
				case '?':
					hi = 1
				case '{':
					lo, hi, fixed, err = s.bounds()
					if err != nil {
						return nil, err
					}
				}
				pref := int8(1)
				if s.peek() == '?' {
					s.at++
					pref = -1
				}
				if fixed {
					pref = s.nodes[n].pref
				}
				n, err = s.repeat(n, lo, hi, pref)
				if err != nil {
					return nil, err
				}
			}
		}
		if err := s.add(n); err != nil {
			return nil, err
		}
	}
	if len(s.frames) != 1 {
		return nil, similarCompileError("parentheses () not balanced")
	}
	root, err := s.finish()
	if err != nil {
		return nil, err
	}
	if err = s.lower(root); err != nil {
		return nil, err
	}
	// Parser arrays are no longer owned by the execution. Retain IR/classes
	// and the canonical bytes held by the owning execution's active cache.
	budget.used = int(unsafe.Sizeof(SimilarProgram{})) + len(pattern) + cap(p.code)*int(unsafe.Sizeof(similarInst{})) + cap(p.classes)*int(unsafe.Sizeof(similarClassEntry{})) + cap(p.preferences)
	budget.runtime = true
	budget.work = nil
	p.budget = budget
	return p, ctx.Err()
}
func (s *similarParser) bounds() (int, int, bool, error) {
	read := func() (int, error) {
		n := 0
		for s.peek() >= '0' && s.peek() <= '9' {
			if err := s.work.step(); err != nil {
				return 0, err
			}
			c := int(s.read() - '0')
			if n > 255/10 || n*10+c > 255 {
				return 0, similarCompileError("invalid repetition count(s)")
			}
			n = n*10 + c
		}
		return n, nil
	}
	lo, err := read()
	if err != nil {
		return 0, 0, false, err
	}
	hi := lo
	fixed := true
	if s.peek() == ',' {
		s.at++
		fixed = false
		hi = -1
		if s.peek() >= '0' && s.peek() <= '9' {
			hi, err = read()
			if err != nil {
				return 0, 0, false, err
			}
		}
	}
	if s.peek() != '}' {
		if s.peek() == -1 {
			return 0, 0, false, similarCompileError("braces {} not balanced")
		}
		return 0, 0, false, similarCompileError("invalid repetition count(s)")
	}
	s.at++
	if hi >= 0 && lo > hi {
		return 0, 0, false, similarCompileError("invalid repetition count(s)")
	}
	return lo, hi, fixed, nil
}

func similarDigit(r rune) int {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0')
	case r >= 'a' && r <= 'f':
		return int(r-'a') + 10
	case r >= 'A' && r <= 'F':
		return int(r-'A') + 10
	}
	return -1
}
func (s *similarParser) digits(base, minlen, maxlen int) (uint32, int, error) {
	var n uint32
	count := 0
	for count < maxlen {
		d := similarDigit(s.peek())
		if d < 0 || d >= base {
			break
		}
		if err := s.work.step(); err != nil {
			return 0, 0, err
		}
		s.read()
		n = n*uint32(base) + uint32(d)
		count++
	}
	if count < minlen {
		return 0, 0, similarCompileError("invalid escape \\ sequence")
	}
	return n, count, nil
}
func (s *similarParser) escape(inclass bool) (similarNode, error) {
	bad := func() (similarNode, error) { return similarNode{}, similarCompileError("invalid escape \\ sequence") }
	if s.peek() == -1 {
		return bad()
	}
	c := s.read()
	n := similarNode{op: similarLiteral, value: c}
	switch c {
	case 'a':
		n.value = 7
	case 'b':
		n.value = 8
	case 'B':
		n.value = '\\'
	case 'c':
		if s.peek() == -1 {
			return bad()
		}
		n.value = s.read() & 31
	case 'e':
		n.value = 27
	case 'f':
		n.value = 12
	case 'n':
		n.value = 10
	case 'r':
		n.value = 13
	case 't':
		n.value = 9
	case 'v':
		n.value = 11
	case 'A':
		n.op = similarBegin
	case 'Z':
		n.op = similarEnd
	case 'm':
		n.op = similarWordBegin
	case 'M':
		n.op = similarWordEnd
	case 'y':
		n.op = similarWordBoundary
	case 'Y':
		n.op = similarNotWordBoundary
	case 'd', 'D', 's', 'S', 'w', 'W':
		class := similarDigitClass
		if c == 's' || c == 'S' {
			class = similarSpaceClass
		}
		if c == 'w' || c == 'W' {
			class = similarWordClass
		}
		n.op = similarClass
		n.value = 0 // Set-level negation is independent of the escaped class.
		n.lo = len(s.program.classes)
		n.hi = n.lo + 1
		if err := similarPush(s.budget, &s.program.classes, similarClassEntry{class: class, invert: c == 'D' || c == 'S' || c == 'W'}); err != nil {
			return similarNode{}, err
		}
	case 'u', 'U', 'x':
		low, high := 4, 4
		if c == 'U' {
			low = 8
			high = 8
		}
		if c == 'x' {
			low = 1
			high = 255
		}
		v, _, err := s.digits(16, low, high)
		if err != nil {
			return similarNode{}, err
		}
		if v > 0x7fffffff {
			return bad()
		}
		n.value = rune(v)
	default:
		if c >= '0' && c <= '9' {
			s.at--
			start := s.at
			if c != '0' {
				v, count, err := s.digits(10, 1, 255)
				if err != nil {
					return similarNode{}, err
				}
				if count == 1 || v > 0 && v <= uint32(s.captures) {
					if inclass {
						return bad()
					}
					if v != 1 || !s.closed {
						return similarNode{}, similarCompileError("invalid backreference number")
					}
					n.op = similarBackref
					return n, nil
				}
				s.at = start
			}
			v, _, err := s.digits(8, 1, 3)
			if err != nil {
				return similarNode{}, err
			}
			if v > 255 {
				s.at--
				v >>= 3
			}
			n.value = rune(v)
		} else if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			return bad()
		}
	}
	if inclass && n.op != similarLiteral && n.op != similarClass {
		return bad()
	}
	return n, nil
}
