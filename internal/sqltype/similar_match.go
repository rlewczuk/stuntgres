package sqltype

import (
	"context"
	"unicode/utf8"
)

func (p *SimilarProgram) Match(ctx context.Context, source string) (matched bool, start, end int, err error) {
	if err = ctx.Err(); err != nil {
		return false, -1, -1, err
	}
	p.matchWork = similarWork{ctx: ctx}
	p.budget.work = &p.matchWork
	defer func() { p.budget.work = nil; p.matchWork.ctx = nil }()
	if p.capture {
		return p.matchSimilarTagged(&p.matchWork, source)
	}
	yes, e := p.matchSimilarStates(&p.matchWork, source)
	return yes, -1, -1, e
}
func (p *SimilarProgram) assertion(op similarOp, source string, pos int) bool {
	if op == similarBegin {
		return pos == 0
	}
	if op == similarEnd {
		return pos == len(source)
	}
	left, right := false, false
	if pos > 0 {
		r, _ := utf8.DecodeLastRuneInString(source[:pos])
		left = p.classMember(r, similarWordClass)
	}
	if pos < len(source) {
		r, _ := utf8.DecodeRuneInString(source[pos:])
		right = p.classMember(r, similarWordClass)
	}
	switch op {
	case similarWordBegin:
		return !left && right
	case similarWordEnd:
		return left && !right
	case similarWordBoundary:
		return left != right
	case similarNotWordBoundary:
		return left == right
	}
	return false
}
func (p *SimilarProgram) nextGeneration(w *similarWork) error {
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
func (p *SimilarProgram) closureSimilar(w *similarWork, source string, pos, pc int, list *[]int) error {
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
		case similarSplit:
			p.stack = append(p.stack, i.x, i.y)
		case similarBegin, similarEnd, similarWordBegin, similarWordEnd, similarWordBoundary, similarNotWordBoundary:
			if p.assertion(i.op, source, pos) {
				p.stack = append(p.stack, i.x)
			}
		default:
			*list = append(*list, pc)
		}
	}
	return nil
}
func (p *SimilarProgram) matchSimilarStates(w *similarWork, source string) (bool, error) {
	var err error
	n := len(p.code)
	p.current, err = similarGrow(&p.budget, p.current, n)
	if err != nil {
		return false, err
	}
	p.next, err = similarGrow(&p.budget, p.next, n)
	if err != nil {
		return false, err
	}
	p.stack, err = similarGrow(&p.budget, p.stack, 2*n+1)
	if err != nil {
		return false, err
	}
	p.seen, err = similarGrow(&p.budget, p.seen, n)
	if err != nil {
		return false, err
	}
	p.seen = p.seen[:n]
	p.current = p.current[:0]
	p.next = p.next[:0]
	if err = p.nextGeneration(w); err != nil {
		return false, err
	}
	if err = p.closureSimilar(w, source, 0, p.start, &p.current); err != nil {
		return false, err
	}
	for pos := 0; pos < len(source); {
		if err = w.step(); err != nil {
			return false, err
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
			yes := i.op == similarAny || i.op == similarLiteral && i.value == r
			if i.op == similarClass {
				yes, err = p.matchesClass(w, i, r)
				if err != nil {
					return false, err
				}
			}
			if yes {
				if err = p.closureSimilar(w, source, nextpos, i.x, &p.next); err != nil {
					return false, err
				}
			}
		}
		p.current, p.next = p.next, p.current
		pos = nextpos
		if len(p.current) == 0 {
			return false, nil
		}
	}
	for _, pc := range p.current {
		if err = w.step(); err != nil {
			return false, err
		}
		if p.code[pc].op == similarAccept {
			return true, w.ctx.Err()
		}
	}
	return false, w.ctx.Err()
}

// Tagged records retain complete preference histories and capture bounds.
// No pc/position-only merge is valid here: a future backreference can make
// an otherwise lower-ranked capture the only feasible accepting path. The
// open-addressed visited set therefore keys every tag, including open tags.
func similarStateHash(w *similarWork, record []int) (uint64, error) {
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
func (p *SimilarProgram) taggedTable(w *similarWork, stride int) error {
	old := p.table
	count := len(p.tagged) / stride
	if len(old) > 0 && count*2 < len(old) {
		return nil
	}
	n := max(16, len(old)*2)
	if n > p.budget.limit/similarIntBytes {
		return similarRuntimeError()
	}
	if err := p.budget.reserve(n * similarIntBytes); err != nil {
		return err
	}
	table := make([]int, n)
	for id := range count {
		record := p.tagged[id*stride : (id+1)*stride]
		hash, err := similarStateHash(w, record)
		if err != nil {
			p.budget.used -= n * similarIntBytes
			return err
		}
		slot := int(hash & uint64(n-1))
		for table[slot] != 0 {
			if err := w.step(); err != nil {
				p.budget.used -= n * similarIntBytes
				return err
			}
			slot = (slot + 1) & (n - 1)
		}
		table[slot] = id + 1
	}
	p.budget.used -= cap(old) * similarIntBytes
	p.table = table
	return nil
}
func (p *SimilarProgram) taggedInsert(w *similarWork, record []int) error {
	stride := len(record)
	if err := p.taggedTable(w, stride); err != nil {
		return err
	}
	hash, err := similarStateHash(w, record)
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
	next, err := similarGrow(&p.budget, p.tagged, len(p.tagged)+stride)
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
func (p *SimilarProgram) betterSimilar(w *similarWork, a, b []int) (bool, error) {
	base := 5 + 2*len(p.preferences) + p.progress
	for rank, pref := range p.preferences {
		if err := w.step(); err != nil {
			return false, err
		}
		comparison, err := p.compareSimilarHistory(w, a[base+rank], b[base+rank], pref)
		if err != nil {
			return false, err
		}
		if comparison != 0 {
			return comparison > 0, nil
		}
	}
	// Equal hierarchical spans have no distinct SQL preference. Use stable
	// byte-boundary tie breaking, independent of alternation traversal order.
	if a[3] != b[3] {
		return a[3] < b[3], nil
	}
	return a[4] > b[4], nil
}
func (p *SimilarProgram) matchSimilarTagged(w *similarWork, source string) (bool, int, int, error) {
	historyBase := 5 + 2*len(p.preferences) + p.progress
	stride := historyBase + len(p.preferences)
	var err error
	p.current, err = similarGrow(&p.budget, p.current, stride)
	if err != nil {
		return false, -1, -1, err
	}
	p.current = p.current[:stride]
	for i := range p.current {
		if err = w.step(); err != nil {
			return false, -1, -1, err
		}
		p.current[i] = -1
	}
	for at := historyBase; at < stride; at++ {
		if err = w.step(); err != nil {
			return false, -1, -1, err
		}
		p.current[at] = 0
	}
	p.histories = p.histories[:0]
	for at := range p.historyTable {
		if err = w.step(); err != nil {
			return false, -1, -1, err
		}
		p.historyTable[at] = 0
	}
	p.current[0] = p.start
	p.current[1] = 0
	p.current[2] = 0
	p.tagged = p.tagged[:0]
	for i := range p.table {
		if err = w.step(); err != nil {
			return false, -1, -1, err
		}
		p.table[i] = 0
	}
	if err = p.taggedInsert(w, p.current); err != nil {
		return false, -1, -1, err
	}
	best := -1
	for id := 0; id < len(p.tagged)/stride; id++ {
		if err = w.step(); err != nil {
			return false, -1, -1, err
		}
		offset := id * stride
		for k := range p.current {
			if err = w.step(); err != nil {
				return false, -1, -1, err
			}
			p.current[k] = p.tagged[offset+k]
		}
		pc, pos := p.current[0], p.current[1]
		i := p.code[pc]
		if i.op == similarAccept {
			if pos == len(source) {
				if best < 0 {
					best = id
				} else {
					better, e := p.betterSimilar(w, p.current, p.tagged[best*stride:(best+1)*stride])
					if e != nil {
						return false, -1, -1, e
					}
					if better {
						best = id
					}
				}
			}
			continue
		}
		nextpos, nextchar := pos, p.current[2]
		yes := true
		switch i.op {
		case similarSplit:
			p.current[0] = i.y
			if err = p.taggedInsert(w, p.current); err != nil {
				return false, -1, -1, err
			}
		case similarTag:
			at := 3 + int(i.value)
			p.current[at] = pos
			if i.value >= 2 {
				p.current[at] = nextchar
			}
			if i.value%2 == 0 {
				p.current[at+1] = -1
			} else if i.value >= 3 {
				rank := int(i.value-3) / 2
				history, e := p.appendSimilarHistory(w, p.current[historyBase+rank], p.current[at]-p.current[at-1])
				if e != nil {
					return false, -1, -1, e
				}
				p.current[historyBase+rank] = history
			}
		case similarReset:
			if i.value != 0 {
				p.current[3] = -1
				p.current[4] = -1
			}
			for rank := i.lo; rank < i.hi; rank++ {
				if err = w.step(); err != nil {
					return false, -1, -1, err
				}
				p.current[5+2*rank] = -1
				p.current[6+2*rank] = -1
			}
		case similarProgressBegin:
			p.current[5+2*len(p.preferences)+int(i.value)] = nextchar
		case similarProgressEnd:
			began := p.current[5+2*len(p.preferences)+int(i.value)]
			if began == nextchar {
				// Only the first, otherwise optional empty iteration is useful:
				// set participating empty captures, then leave the repetition.
				if i.hi != 0 || p.current[5+2*i.lo] != nextchar {
					yes = false
				} else {
					i.x = i.y
				}
			}
		case similarBegin, similarEnd, similarWordBegin, similarWordEnd, similarWordBoundary, similarNotWordBoundary:
			yes = p.assertion(i.op, source, pos)
		case similarBackref:
			start, end := p.current[3], p.current[4]
			yes = start >= 0 && end >= start && end-start <= len(source)-pos
			if yes {
				for k := range end - start {
					if err = w.step(); err != nil {
						return false, -1, -1, err
					}
					if source[start+k] != source[pos+k] {
						yes = false
						break
					}
					if source[start+k]&0xc0 != 0x80 {
						nextchar++
					}
				}
				nextpos = pos + end - start
			}
		default:
			if pos == len(source) {
				yes = false
			} else {
				r, width := utf8.DecodeRuneInString(source[pos:])
				nextpos = pos + width
				nextchar++
				yes = i.op == similarAny || i.op == similarLiteral && i.value == r
				if i.op == similarClass {
					yes, err = p.matchesClass(w, i, r)
					if err != nil {
						return false, -1, -1, err
					}
				}
			}
		}
		if yes {
			p.current[0] = i.x
			p.current[1] = nextpos
			p.current[2] = nextchar
			if err = p.taggedInsert(w, p.current); err != nil {
				return false, -1, -1, err
			}
		}
	}
	if err = w.ctx.Err(); err != nil {
		return false, -1, -1, err
	}
	if best < 0 {
		return false, -1, -1, nil
	}
	record := p.tagged[best*stride : (best+1)*stride]
	return true, record[3], record[4], nil
}
