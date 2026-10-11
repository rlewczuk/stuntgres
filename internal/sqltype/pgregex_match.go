package sqltype

import (
	"context"
	"unicode/utf8"
)

func (p *PGRegexProgram) Match(ctx context.Context, source string, wantCaptures bool) (PGRegexMatch, error) {
	if err := ctx.Err(); err != nil {
		return PGRegexMatch{}, err
	}
	p.matchWork = pgRegexWork{ctx: ctx}
	p.budget.work = &p.matchWork
	defer func() { p.budget.work = nil; p.matchWork.ctx = nil }()
	if wantCaptures || p.backrefs {
		return p.matchPGRegexTagged(&p.matchWork, source, wantCaptures)
	}
	yes, err := p.matchPGRegexStates(&p.matchWork, source)
	return PGRegexMatch{Matched: yes, Whole: PGRegexSpan{-1, -1}}, err
}
func (p *PGRegexProgram) assertion(op pgRegexOp, source string, pos int) bool {
	if op == pgRegexAbsoluteBegin {
		return pos == 0
	}
	if op == pgRegexAbsoluteEnd {
		return pos == len(source)
	}
	if op == pgRegexBegin {
		return pos == 0 || p.newlineAnchor && source[pos-1] == '\n'
	}
	if op == pgRegexEnd {
		return pos == len(source) || p.newlineAnchor && source[pos] == '\n'
	}
	left, right := false, false
	if pos > 0 {
		r, _ := utf8.DecodeLastRuneInString(source[:pos])
		left = p.classMember(r, pgRegexWordClass)
	}
	if pos < len(source) {
		r, _ := utf8.DecodeRuneInString(source[pos:])
		right = p.classMember(r, pgRegexWordClass)
	}
	switch op {
	case pgRegexWordBegin:
		return !left && right
	case pgRegexWordEnd:
		return left && !right
	case pgRegexWordBoundary:
		return left != right
	case pgRegexNotWordBoundary:
		return left == right
	}
	return false
}
func (p *PGRegexProgram) nextGeneration(w *pgRegexWork) error {
	p.generation++
	if p.generation == 0 {
		for i := range p.seen {
			if err := w.step(); err != nil {
				return err
			}
			p.seen[i] = 0
		}
		p.generation = 1
	}
	return nil
}
func (p *PGRegexProgram) closurePGRegex(w *pgRegexWork, source string, pos, pc int, list *[]int) error {
	p.stack = p.stack[:0]
	p.stack = append(p.stack, pc)
	for len(p.stack) > 0 {
		if err := w.step(); err != nil {
			return err
		}
		pc = p.stack[len(p.stack)-1]
		p.stack = p.stack[:len(p.stack)-1]
		if p.seen[pc] == p.generation {
			continue
		}
		p.seen[pc] = p.generation
		i := p.code[pc]
		switch i.op {
		case pgRegexSplit:
			p.stack = append(p.stack, i.x, i.y)
		case pgRegexTag, pgRegexReset, pgRegexProgressBegin, pgRegexProgressEnd:
			p.stack = append(p.stack, i.x)
		case pgRegexLook:
			yes, err := p.evaluateLook(w, source, i, pos)
			if err != nil {
				return err
			}
			if yes {
				p.stack = append(p.stack, i.x)
			}
		case pgRegexBegin, pgRegexEnd, pgRegexAbsoluteBegin, pgRegexAbsoluteEnd, pgRegexWordBegin, pgRegexWordEnd, pgRegexWordBoundary, pgRegexNotWordBoundary:
			if p.assertion(i.op, source, pos) {
				p.stack = append(p.stack, i.x)
			}
		default:
			*list = append(*list, pc)
		}
	}
	return nil
}
func (p *PGRegexProgram) matchPGRegexStates(w *pgRegexWork, source string) (bool, error) {
	var err error
	n := len(p.code)
	p.current, err = pgRegexGrow(&p.budget, p.current, n)
	if err != nil {
		return false, err
	}
	p.next, err = pgRegexGrow(&p.budget, p.next, n)
	if err != nil {
		return false, err
	}
	p.stack, err = pgRegexGrow(&p.budget, p.stack, 2*n+1)
	if err != nil {
		return false, err
	}
	p.seen, err = pgRegexGrow(&p.budget, p.seen, n)
	if err != nil {
		return false, err
	}
	p.seen = p.seen[:n]
	p.current = p.current[:0]
	p.next = p.next[:0]
	if err = p.nextGeneration(w); err != nil {
		return false, err
	}
	for pos := 0; ; {
		if err = w.step(); err != nil {
			return false, err
		}
		if err = p.closurePGRegex(w, source, pos, p.start, &p.current); err != nil {
			return false, err
		}
		for _, pc := range p.current {
			if err = w.step(); err != nil {
				return false, err
			}
			if p.code[pc].op == pgRegexAccept {
				return true, w.ctx.Err()
			}
		}
		if pos == len(source) {
			return false, w.ctx.Err()
		}
		r, width := utf8.DecodeRuneInString(source[pos:])
		nextpos := pos + width
		p.next = p.next[:0]
		if err = p.nextGeneration(w); err != nil {
			return false, err
		}
		for _, pc := range p.current {
			if err = w.step(); err != nil {
				return false, err
			}
			i := p.code[pc]
			yes, e := p.matchCharacter(w, i, r)
			if e != nil {
				return false, e
			}
			if yes {
				if err = p.closurePGRegex(w, source, nextpos, i.x, &p.next); err != nil {
					return false, err
				}
			}
		}
		p.current, p.next = p.next, p.current
		pos = nextpos
	}
}

// Tagged records retain complete preference histories and capture bounds.
// No pc/position-only merge is valid here: a future backreference can make
// an otherwise lower-ranked capture the only feasible accepting path. The
// open-addressed visited set therefore keys every tag, including open tags.
func pgRegexStateHash(w *pgRegexWork, record []int) (uint64, error) {
	h := uint64(1469598103934665603)
	for _, v := range record {
		if err := w.step(); err != nil {
			return 0, err
		}
		h ^= uint64(v)
		h *= 1099511628211
		h ^= h >> 32
	}
	return h, nil
}
func (p *PGRegexProgram) taggedTable(w *pgRegexWork, stride int) error {
	old := p.table
	count := len(p.tagged) / stride
	if len(old) > 0 && count*2 < len(old) {
		return nil
	}
	n := max(16, len(old)*2)
	if n > p.budget.limit/pgRegexIntBytes {
		return pgRegexRuntimeError()
	}
	if err := p.budget.reserve(n * pgRegexIntBytes); err != nil {
		return err
	}
	table := make([]int, n)
	for id := range count {
		record := p.tagged[id*stride : (id+1)*stride]
		hash, err := pgRegexStateHash(w, record)
		if err != nil {
			p.budget.used -= n * pgRegexIntBytes
			return err
		}
		slot := int(hash & uint64(n-1))
		for table[slot] != 0 {
			if err := w.step(); err != nil {
				p.budget.used -= n * pgRegexIntBytes
				return err
			}
			slot = (slot + 1) & (n - 1)
		}
		table[slot] = id + 1
	}
	p.budget.used -= cap(old) * pgRegexIntBytes
	p.table = table
	return nil
}
func (p *PGRegexProgram) taggedInsert(w *pgRegexWork, record []int) error {
	stride := len(record)
	if err := p.taggedTable(w, stride); err != nil {
		return err
	}
	hash, err := pgRegexStateHash(w, record)
	if err != nil {
		return err
	}
	slot := int(hash & uint64(len(p.table)-1))
	for p.table[slot] != 0 {
		if err := w.step(); err != nil {
			return err
		}
		id := p.table[slot] - 1
		same := true
		for k, v := range record {
			if err := w.step(); err != nil {
				return err
			}
			if p.tagged[id*stride+k] != v {
				same = false
				break
			}
		}
		if same {
			return nil
		}
		slot = (slot + 1) & (len(p.table) - 1)
	}
	next, err := pgRegexGrow(&p.budget, p.tagged, len(p.tagged)+stride)
	if err != nil {
		return err
	}
	p.tagged = next
	id := len(p.tagged) / stride
	for _, v := range record {
		if err := w.step(); err != nil {
			return err
		}
		p.tagged = append(p.tagged, v)
	}
	p.table[slot] = id + 1
	return nil
}
func (p *PGRegexProgram) betterPGRegex(w *pgRegexWork, a, b []int) (bool, error) {
	if a[2] != b[2] {
		if p.preference < 0 {
			return a[2] < b[2], nil
		}
		return a[2] > b[2], nil
	}
	base := 3 + 2*p.captures + 2*len(p.preferences) + p.progress
	for rank, pref := range p.preferences {
		if err := w.step(); err != nil {
			return false, err
		}
		comparison, err := p.comparePGRegexHistory(w, a[base+rank], b[base+rank], pref)
		if err != nil {
			return false, err
		}
		if comparison != 0 {
			return comparison > 0, nil
		}
	}
	for k := 0; k < p.captures; k++ {
		at := 3 + 2*k
		if a[at] != b[at] {
			return a[at] >= 0 && (b[at] < 0 || a[at] < b[at]), nil
		}
		if a[at+1] != b[at+1] {
			return a[at+1] > b[at+1], nil
		}
	}
	return false, nil
}
func (p *PGRegexProgram) matchPGRegexTagged(w *pgRegexWork, source string, wantCaptures bool) (PGRegexMatch, error) {
	captureBase := 3
	rankBase := captureBase + 2*p.captures
	progressBase := rankBase + 2*len(p.preferences)
	historyBase := progressBase + p.progress
	stride := historyBase + len(p.preferences)
	var err error
	p.current, err = pgRegexGrow(&p.budget, p.current, stride)
	if err != nil {
		return PGRegexMatch{}, err
	}
	p.current = p.current[:stride]
	for search := 0; ; {
		for k := range p.current {
			if err = w.step(); err != nil {
				return PGRegexMatch{}, err
			}
			p.current[k] = -1
		}
		for k := historyBase; k < stride; k++ {
			p.current[k] = 0
		}
		p.histories = p.histories[:0]
		for k := range p.historyTable {
			if err = w.step(); err != nil {
				return PGRegexMatch{}, err
			}
			p.historyTable[k] = 0
		}
		for k := range p.table {
			if err = w.step(); err != nil {
				return PGRegexMatch{}, err
			}
			p.table[k] = 0
		}
		p.current[0], p.current[1], p.current[2] = p.start, search, 0
		p.tagged = p.tagged[:0]
		if err = p.taggedInsert(w, p.current); err != nil {
			return PGRegexMatch{}, err
		}
		best := -1
		for id := 0; id < len(p.tagged)/stride; id++ {
			if err = w.step(); err != nil {
				return PGRegexMatch{}, err
			}
			offset := id * stride
			for k := range p.current {
				if err = w.step(); err != nil {
					return PGRegexMatch{}, err
				}
				p.current[k] = p.tagged[offset+k]
			}
			pc, pos := p.current[0], p.current[1]
			i := p.code[pc]
			if i.op == pgRegexAccept {
				if !wantCaptures {
					return PGRegexMatch{Matched: true, Whole: PGRegexSpan{-1, -1}}, w.ctx.Err()
				}
				if best < 0 {
					best = id
				} else {
					better, e := p.betterPGRegex(w, p.current, p.tagged[best*stride:(best+1)*stride])
					if e != nil {
						return PGRegexMatch{}, e
					}
					if better {
						best = id
					}
				}
				continue
			}
			nextpos, nextchar, yes := pos, p.current[2], true
			switch i.op {
			case pgRegexSplit:
				p.current[0] = i.y
				if err = p.taggedInsert(w, p.current); err != nil {
					return PGRegexMatch{}, err
				}
			case pgRegexTag:
				at := captureBase + int(i.value)
				p.current[at] = pos
				if int(i.value) >= 2*p.captures {
					p.current[at] = nextchar
				}
				if i.value%2 == 0 {
					p.current[at+1] = -1
				} else if wantCaptures && int(i.value) >= 2*p.captures {
					rank := (int(i.value) - 2*p.captures - 1) / 2
					history, e := p.appendPGRegexHistory(w, p.current[historyBase+rank], p.current[at]-p.current[at-1])
					if e != nil {
						return PGRegexMatch{}, e
					}
					p.current[historyBase+rank] = history
				}
			case pgRegexReset:
				for k := i.captureLo; k < i.captureHi; k++ {
					if err = w.step(); err != nil {
						return PGRegexMatch{}, err
					}
					p.current[captureBase+2*k], p.current[captureBase+2*k+1] = -1, -1
				}
				for k := i.lo; k < i.hi; k++ {
					if err = w.step(); err != nil {
						return PGRegexMatch{}, err
					}
					p.current[rankBase+2*k], p.current[rankBase+2*k+1] = -1, -1
				}
			case pgRegexProgressBegin:
				p.current[progressBase+int(i.value)] = nextchar
			case pgRegexProgressEnd:
				began := p.current[progressBase+int(i.value)]
				if began == nextchar {
					if i.hi != 0 || p.current[rankBase+2*i.lo] != nextchar {
						yes = false
					} else {
						i.x = i.y
					}
				}
			case pgRegexBegin, pgRegexEnd, pgRegexAbsoluteBegin, pgRegexAbsoluteEnd, pgRegexWordBegin, pgRegexWordEnd, pgRegexWordBoundary, pgRegexNotWordBoundary:
				yes = p.assertion(i.op, source, pos)
			case pgRegexLook:
				yes, err = p.evaluateLook(w, source, i, pos)
				if err != nil {
					return PGRegexMatch{}, err
				}
			case pgRegexBackref:
				at := captureBase + 2*(int(i.value)-1)
				var consumed int
				yes, nextpos, consumed, err = p.matchBackreference(w, source, p.current[at], p.current[at+1], pos)
				if err != nil {
					return PGRegexMatch{}, err
				}
				nextchar += consumed
			default:
				if pos == len(source) {
					yes = false
				} else {
					r, width := utf8.DecodeRuneInString(source[pos:])
					nextpos += width
					nextchar++
					yes, err = p.matchCharacter(w, i, r)
					if err != nil {
						return PGRegexMatch{}, err
					}
				}
			}
			if yes {
				p.current[0], p.current[1], p.current[2] = i.x, nextpos, nextchar
				if err = p.taggedInsert(w, p.current); err != nil {
					return PGRegexMatch{}, err
				}
			}
		}
		if err = w.ctx.Err(); err != nil {
			return PGRegexMatch{}, err
		}
		if best >= 0 {
			record := p.tagged[best*stride : (best+1)*stride]
			result := PGRegexMatch{Matched: true, Whole: PGRegexSpan{search, record[1]}}
			if wantCaptures && p.captures > 0 {
				p.spans, err = pgRegexGrow(&p.budget, p.spans, p.captures)
				if err != nil {
					return PGRegexMatch{}, err
				}
				p.spans = p.spans[:p.captures]
				for k := range p.spans {
					if err = w.step(); err != nil {
						return PGRegexMatch{}, err
					}
					p.spans[k] = PGRegexSpan{record[captureBase+2*k], record[captureBase+2*k+1]}
				}
				result.Captures = p.spans
			}
			return result, w.ctx.Err()
		}
		if search == len(source) {
			return PGRegexMatch{Whole: PGRegexSpan{-1, -1}}, w.ctx.Err()
		}
		_, width := utf8.DecodeRuneInString(source[search:])
		search += width
	}
}
