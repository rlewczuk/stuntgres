package sqltype

import (
	"context"
	"unsafe"
)

const pgRegexMemoryLimit = 64 << 20

func pgRegexCompileError(reason string) error {
	return failure("2201B", "invalid regular expression: "+reason)
}
func pgRegexRuntimeError() error {
	return failure("2201B", "regular expression failed: regular expression is too complex")
}

type pgRegexWork struct {
	ctx   context.Context
	ticks uint32
}

func (w *pgRegexWork) step() error {
	w.ticks++
	if w.ticks&1023 == 0 {
		return w.ctx.Err()
	}
	return nil
}

const pgRegexIntBytes = int(unsafe.Sizeof(int(0)))

type pgRegexBudget struct {
	used, limit int
	runtime     bool
	work        *pgRegexWork
}

func (b *pgRegexBudget) reserve(n int) error {
	if n < 0 || n > b.limit-b.used {
		if b.runtime {
			return pgRegexRuntimeError()
		}
		return pgRegexCompileError("regular expression is too complex")
	}
	b.used += n
	return nil
}

// Account both old and replacement backing arrays while copying. Growth and
// long copies are cancellation-aware, and no append may implicitly allocate.
func pgRegexGrow[T any](b *pgRegexBudget, s []T, need int) ([]T, error) {
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
func pgRegexPush[T any](b *pgRegexBudget, s *[]T, v T) error {
	next, err := pgRegexGrow(b, *s, len(*s)+1)
	if err != nil {
		return err
	}
	*s = append(next, v)
	return nil
}

type pgRegexOp uint8

const (
	pgRegexEmpty pgRegexOp = iota
	pgRegexLiteral
	pgRegexAny
	pgRegexClass
	pgRegexBegin
	pgRegexEnd
	pgRegexWordBegin
	pgRegexWordEnd
	pgRegexWordBoundary
	pgRegexNotWordBoundary
	pgRegexBackref
	pgRegexConcat
	pgRegexAlt
	pgRegexRepeat
	pgRegexGroup
	pgRegexSplit
	pgRegexTag
	pgRegexReset
	pgRegexProgressBegin
	pgRegexProgressEnd
	pgRegexAccept
	pgRegexAbsoluteBegin
	pgRegexAbsoluteEnd
	pgRegexLook
)

type pgRegexNode struct {
	op                                         pgRegexOp
	left, right                                int
	lo, hi                                     int
	value                                      rune
	pref                                       int8
	capture, captureBelow, backrefBelow, plain bool
	rank, rankLo, rankHi, progress             int
	group, captureLo, captureHi                int
}
type pgRegexInst struct {
	op                   pgRegexOp
	x, y                 int
	value                rune
	lo, hi               int
	captureLo, captureHi int
}
type pgRegexFrame struct {
	seq, alt, atom int
	capture        int
	look, ownLook  bool
	lookKind       rune
	quantified     bool
}

// PGRegexProgram has immutable instructions and execution-local reusable scratch.
// An owning execution must not call Match concurrently. No input is retained.
type PGRegexProgram struct {
	code                                    []pgRegexInst
	classes                                 []pgRegexClassEntry
	classRanges                             []pgRegexRange
	start                                   int
	collation                               CollationRef
	compiledBytes                           int
	capture                                 bool
	captures                                int
	backrefs                                bool
	options                                 PGRegexOptions
	newlineStop, newlineAnchor              bool
	preference                              int8
	spans                                   []PGRegexSpan
	lookScratch                             []pgRegexLookState
	lookFrames                              []pgRegexLookFrame
	preferences                             []int8
	progress                                int
	budget                                  pgRegexBudget
	matchWork                               pgRegexWork
	current, next, stack                    []int
	seen                                    []uint32
	generation                              uint32
	tagged                                  []int
	table                                   []int
	histories                               []pgRegexHistory
	historyTable, historyLeft, historyRight []int
}

// HasCapture reports numbered-group metadata, including groups under {0}.
func (p *PGRegexProgram) HasCapture() bool { return p.capture }

type pgRegexParser struct {
	nodes    []pgRegexNode
	frames   []pgRegexFrame
	program  *PGRegexProgram
	budget   *pgRegexBudget
	work     *pgRegexWork
	captures int
	closed   []bool
}

func (s *pgRegexParser) node(n pgRegexNode) (int, error) {
	n.rank = -1
	n.rankLo = -1
	n.progress = -1
	n.captureBelow = n.capture
	n.backrefBelow = n.op == pgRegexBackref
	if !n.plain {
		if n.op == pgRegexGroup || n.op == pgRegexRepeat || n.op == pgRegexConcat || n.op == pgRegexAlt {
			n.captureBelow = n.captureBelow || s.nodes[n.left].captureBelow
			n.backrefBelow = n.backrefBelow || s.nodes[n.left].backrefBelow
		}
		if n.op == pgRegexConcat || n.op == pgRegexAlt {
			n.captureBelow = n.captureBelow || s.nodes[n.right].captureBelow
			n.backrefBelow = n.backrefBelow || s.nodes[n.right].backrefBelow
		}
	}
	n.captureLo, n.captureHi = -1, -1
	if n.capture {
		n.captureLo, n.captureHi = n.group-1, n.group
	}
	if !n.plain && (n.op == pgRegexGroup || n.op == pgRegexRepeat || n.op == pgRegexConcat || n.op == pgRegexAlt) {
		children := [2]int{n.left, -1}
		if n.op == pgRegexConcat || n.op == pgRegexAlt {
			children[1] = n.right
		}
		for _, at := range children {
			if at < 0 {
				continue
			}
			c := s.nodes[at]
			if c.captureLo >= 0 {
				if n.captureLo < 0 {
					n.captureLo = c.captureLo
				}
				n.captureLo = min(n.captureLo, c.captureLo)
				n.captureHi = max(n.captureHi, c.captureHi)
			}
		}
	}
	i := len(s.nodes)
	err := pgRegexPush(s.budget, &s.nodes, n)
	return i, err
}

func (s *pgRegexParser) repeat(atom, lo, hi int, pref int8) (int, error) {
	body := s.nodes[atom]
	if lo > 0 && body.captureBelow && !body.backrefBelow {
		// Release regcomp's capture-only path is x{m-1,n-1}x. The plain
		// prefix has priority over the last (capturing) operand, including
		// when a fixed count inherits a nongreedy operand preference.
		end := hi
		if end >= 0 {
			end--
		}
		prefix, err := s.node(pgRegexNode{op: pgRegexRepeat, left: atom, lo: lo - 1, hi: end, pref: pref, plain: true})
		if err != nil {
			return 0, err
		}
		joined, err := s.node(pgRegexNode{op: pgRegexConcat, left: prefix, right: atom, pref: pref})
		if err != nil {
			return 0, err
		}
		return s.node(pgRegexNode{op: pgRegexGroup, left: joined, pref: pref})
	}
	return s.node(pgRegexNode{op: pgRegexRepeat, left: atom, lo: lo, hi: hi, pref: pref})
}
func (s *pgRegexParser) combine(op pgRegexOp, a, b int) (int, error) {
	pref := s.nodes[a].pref
	if op == pgRegexAlt {
		pref = 1
	} else if pref == 0 {
		pref = s.nodes[b].pref
	}
	return s.node(pgRegexNode{op: op, left: a, right: b, pref: pref})
}
func (s *pgRegexParser) add(n int) error {
	f := &s.frames[len(s.frames)-1]
	if f.seq < 0 {
		f.seq = n
		return nil
	}
	v, err := s.combine(pgRegexConcat, f.seq, n)
	if err != nil {
		return err
	}
	f.seq = v
	return nil
}
func (s *pgRegexParser) branch() error {
	f := &s.frames[len(s.frames)-1]
	seq := f.seq
	if seq < 0 {
		var err error
		seq, err = s.node(pgRegexNode{op: pgRegexEmpty})
		if err != nil {
			return err
		}
	}
	if f.alt < 0 {
		f.alt = seq
	} else {
		v, err := s.combine(pgRegexAlt, f.alt, seq)
		if err != nil {
			return err
		}
		f.alt = v
	}
	f.seq = -1
	return nil
}
func (s *pgRegexParser) finish() (int, error) {
	f := s.frames[len(s.frames)-1]
	seq := f.seq
	if seq < 0 {
		var err error
		seq, err = s.node(pgRegexNode{op: pgRegexEmpty})
		if err != nil {
			return 0, err
		}
	}
	if f.alt >= 0 {
		return s.combine(pgRegexAlt, f.alt, seq)
	}
	return seq, nil
}
