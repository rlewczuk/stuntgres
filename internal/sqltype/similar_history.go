package sqltype

// Histories keep operand partitions in first-to-last iteration order, while
// capture tags separately retain only the final iteration. Interning makes
// structurally equal histories equal visited-state keys without unsafe merges.
type similarHistory struct{ previous, length, depth int }

func similarHistoryHash(previous, length int) uint64 {
	h := uint64(previous) * 0x9e3779b97f4a7c15
	h ^= uint64(length) + 0x517cc1b727220a95 + (h << 6) + (h >> 2)
	return h ^ (h >> 32)
}
func (p *SimilarProgram) growSimilarHistoryTable(w *similarWork) error {
	count := len(p.histories)
	old := p.historyTable
	if len(old) > 0 && count*2 < len(old) {
		return nil
	}
	size := max(16, len(old)*2)
	if size > p.budget.limit/similarIntBytes {
		return similarRuntimeError()
	}
	bytes := size * similarIntBytes
	if err := p.budget.reserve(bytes); err != nil {
		return err
	}
	table := make([]int, size)
	for id := range count {
		if err := w.step(); err != nil {
			p.budget.used -= bytes
			return err
		}
		h := p.histories[id]
		slot := int(similarHistoryHash(h.previous, h.length) & uint64(size-1))
		for table[slot] != 0 {
			if err := w.step(); err != nil {
				p.budget.used -= bytes
				return err
			}
			slot = (slot + 1) & (size - 1)
		}
		table[slot] = id + 1
	}
	p.budget.used -= cap(old) * similarIntBytes
	p.historyTable = table
	return nil
}
func (p *SimilarProgram) appendSimilarHistory(w *similarWork, previous, length int) (int, error) {
	if err := p.growSimilarHistoryTable(w); err != nil {
		return 0, err
	}
	slot := int(similarHistoryHash(previous, length) & uint64(len(p.historyTable)-1))
	for p.historyTable[slot] != 0 {
		if err := w.step(); err != nil {
			return 0, err
		}
		id := p.historyTable[slot]
		h := p.histories[id-1]
		if h.previous == previous && h.length == length {
			return id, nil
		}
		slot = (slot + 1) & (len(p.historyTable) - 1)
	}
	depth := 1
	if previous != 0 {
		depth += p.histories[previous-1].depth
	}
	id := len(p.histories) + 1
	if err := similarPush(&p.budget, &p.histories, similarHistory{previous: previous, length: length, depth: depth}); err != nil {
		return 0, err
	}
	p.historyTable[slot] = id
	return id, nil
}
func (p *SimilarProgram) compareSimilarHistory(w *similarWork, a, b int, pref int8) (int, error) {
	if a == b {
		return 0, nil
	}
	na, nb := 0, 0
	if a != 0 {
		na = p.histories[a-1].depth
	}
	if b != 0 {
		nb = p.histories[b-1].depth
	}
	var err error
	p.historyLeft, err = similarGrow(&p.budget, p.historyLeft, na)
	if err != nil {
		return 0, err
	}
	p.historyLeft = p.historyLeft[:na]
	p.historyRight, err = similarGrow(&p.budget, p.historyRight, nb)
	if err != nil {
		return 0, err
	}
	p.historyRight = p.historyRight[:nb]
	for at := na - 1; at >= 0; at-- {
		if err = w.step(); err != nil {
			return 0, err
		}
		h := p.histories[a-1]
		p.historyLeft[at] = h.length
		a = h.previous
	}
	for at := nb - 1; at >= 0; at-- {
		if err = w.step(); err != nil {
			return 0, err
		}
		h := p.histories[b-1]
		p.historyRight[at] = h.length
		b = h.previous
	}
	for at := range min(na, nb) {
		if err = w.step(); err != nil {
			return 0, err
		}
		left, right := p.historyLeft[at], p.historyRight[at]
		if left != right {
			if pref > 0 && left > right || pref < 0 && left < right {
				return 1, nil
			}
			return -1, nil
		}
	}
	if na == nb {
		return 0, nil
	}
	// On an empty target a SHORTER operand admits zero iterations first;
	// a LONGER operand prefers a participating empty iteration over none.
	if pref > 0 && na > nb || pref < 0 && na < nb {
		return 1, nil
	}
	return -1, nil
}
