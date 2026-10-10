package sqltype

// Lowering uses heap-accounted explicit continuations; even hostile nesting
// never consumes the Go call stack. Counted repetitions are expanded only
// after each instruction/continuation allocation has passed accounting.
type similarLowerFrame struct {
	node, next, stage, result, count, split int
	wrapped, plain                          bool
}

func (s *similarParser) instruction(i similarInst) (int, error) {
	at := len(s.program.code)
	err := similarPush(s.budget, &s.program.code, i)
	return at, err
}
func (s *similarParser) lower(root int) error {
	p := s.program
	if p.capture {
		var walk []int
		if err := similarPush(s.budget, &walk, root); err != nil {
			return err
		}
		for len(walk) > 0 {
			if err := s.work.step(); err != nil {
				return err
			}
			at := walk[len(walk)-1]
			walk = walk[:len(walk)-1]
			n := &s.nodes[at]
			if (n.pref != 0 && (n.op == similarGroup || n.op == similarRepeat)) || n.capture {
				// A participating empty subexpression outranks nonparticipation even
				// when its fixed atom has no intrinsic greediness.
				pref := n.pref
				if pref == 0 {
					pref = 1
				}
				n.rank = len(p.preferences)
				if err := similarPush(s.budget, &p.preferences, pref); err != nil {
					return err
				}
			}
			if n.op == similarRepeat && !n.plain && (n.hi < 0 || n.hi > n.lo) {
				n.progress = p.progress
				p.progress++
			}
			if n.plain {
				continue
			}
			if n.op == similarConcat || n.op == similarAlt {
				if err := similarPush(s.budget, &walk, n.right); err != nil {
					return err
				}
			}
			if n.op == similarConcat || n.op == similarAlt || n.op == similarRepeat || n.op == similarGroup {
				if err := similarPush(s.budget, &walk, n.left); err != nil {
					return err
				}
			}
		}
		// Nodes are created postorder; subtree ranks form contiguous preorder
		// intervals. Group entry clears descendants before each fresh iteration.
		for at := range s.nodes {
			if err := s.work.step(); err != nil {
				return err
			}
			n := &s.nodes[at]
			if n.rank >= 0 {
				n.rankLo = n.rank
				n.rankHi = n.rank + 1
			}
			children := [2]int{-1, -1}
			if !n.plain && (n.op == similarConcat || n.op == similarAlt || n.op == similarRepeat || n.op == similarGroup) {
				children[0] = n.left
			}
			if n.op == similarConcat || n.op == similarAlt {
				children[1] = n.right
			}
			for _, child := range children {
				if child < 0 {
					continue
				}
				c := s.nodes[child]
				if c.rankLo >= 0 {
					if n.rankLo < 0 || c.rankLo < n.rankLo {
						n.rankLo = c.rankLo
					}
					n.rankHi = max(n.rankHi, c.rankHi)
				}
			}
		}
	}
	accept, err := s.instruction(similarInst{op: similarAccept})
	if err != nil {
		return err
	}
	var frames []similarLowerFrame
	if err := similarPush(s.budget, &frames, similarLowerFrame{node: root, next: accept}); err != nil {
		return err
	}
	result := accept
	push := func(node, next int, plain bool) error {
		return similarPush(s.budget, &frames, similarLowerFrame{node: node, next: next, plain: plain})
	}
	for len(frames) > 0 {
		if err := s.work.step(); err != nil {
			return err
		}
		at := len(frames) - 1
		f := frames[at]
		n := s.nodes[f.node]
		plainChild := f.plain || n.plain
		if !f.wrapped && !f.plain && p.capture && (n.rank >= 0 || n.capture) {
			next := f.next
			if n.rank >= 0 {
				next, err = s.instruction(similarInst{op: similarTag, x: next, value: rune(3 + 2*n.rank)})
				if err != nil {
					return err
				}
			}
			if n.capture {
				next, err = s.instruction(similarInst{op: similarTag, x: next, value: 1})
				if err != nil {
					return err
				}
			}
			frames[at].wrapped = true
			frames[at].next = next
			continue
		}
		done := false
		switch n.op {
		case similarConcat:
			switch f.stage {
			case 0:
				frames[at].stage = 1
				err = push(n.right, f.next, plainChild)
			case 1:
				frames[at].stage = 2
				err = push(n.left, result, plainChild)
			default:
				done = true
			}
		case similarAlt:
			switch f.stage {
			case 0:
				frames[at].stage = 1
				err = push(n.right, f.next, plainChild)
			case 1:
				frames[at].result = result
				frames[at].stage = 2
				err = push(n.left, f.next, plainChild)
			default:
				result, err = s.instruction(similarInst{op: similarSplit, x: result, y: f.result})
				done = true
			}
		case similarGroup:
			if f.stage == 0 {
				frames[at].stage = 1
				err = push(n.left, f.next, plainChild)
			} else {
				done = true
			}
		case similarRepeat:
			switch f.stage {
			case 0:
				frames[at].result = f.next
				frames[at].count = 0
				if n.hi < 0 {
					split, e := s.instruction(similarInst{op: similarSplit, y: f.next})
					err = e
					frames[at].split = split
					frames[at].stage = 1
					continuation := split
					if err == nil && !f.plain && n.progress >= 0 {
						continuation, err = s.instruction(similarInst{op: similarProgressEnd, x: split, y: f.next, value: rune(n.progress), lo: n.rank, hi: n.lo})
					}
					if err == nil {
						err = push(n.left, continuation, plainChild)
					}
				} else {
					frames[at].stage = 2
				}
			case 1:
				if !f.plain && n.progress >= 0 {
					result, err = s.instruction(similarInst{op: similarProgressBegin, x: result, value: rune(n.progress)})
				}
				p.code[f.split].x = result
				frames[at].result = f.split
				frames[at].stage = 2
			case 2:
				optional := 0
				if n.hi >= 0 {
					optional = n.hi - n.lo
				}
				if f.count < optional {
					frames[at].stage = 3
					continuation := f.result
					if !f.plain && n.progress >= 0 {
						continuation, err = s.instruction(similarInst{op: similarProgressEnd, x: continuation, y: f.next, value: rune(n.progress), lo: n.rank, hi: n.lo})
					}
					if err == nil {
						err = push(n.left, continuation, plainChild)
					}
				} else {
					frames[at].count = 0
					frames[at].stage = 4
				}
			case 3:
				if !f.plain && n.progress >= 0 {
					result, err = s.instruction(similarInst{op: similarProgressBegin, x: result, value: rune(n.progress)})
					if err != nil {
						return err
					}
				}
				split, e := s.instruction(similarInst{op: similarSplit, x: result, y: f.result})
				err = e
				frames[at].result = split
				frames[at].count++
				frames[at].stage = 2
			case 4:
				if f.count < n.lo {
					frames[at].stage = 5
					err = push(n.left, f.result, plainChild)
				} else {
					result = f.result
					done = true
				}
			case 5:
				frames[at].result = result
				frames[at].count++
				frames[at].stage = 4
			}
		case similarEmpty:
			result = f.next
			done = true
		default:
			result, err = s.instruction(similarInst{op: n.op, x: f.next, value: n.value, lo: n.lo, hi: n.hi})
			done = true
		}
		if err != nil {
			return err
		}
		if done {
			if !f.plain && p.capture {
				if n.capture {
					result, err = s.instruction(similarInst{op: similarTag, x: result, value: 0})
					if err != nil {
						return err
					}
				}
				if n.rank >= 0 {
					result, err = s.instruction(similarInst{op: similarTag, x: result, value: rune(2 + 2*n.rank)})
					if err != nil {
						return err
					}
				}
				if n.op == similarGroup && (n.rankLo >= 0 || n.captureBelow) {
					capture := rune(0)
					if n.captureBelow {
						capture = 1
					}
					result, err = s.instruction(similarInst{op: similarReset, x: result, value: capture, lo: n.rankLo, hi: n.rankHi})
					if err != nil {
						return err
					}
				}
			}
			frames = frames[:at]
		}
	}
	p.start = result
	return nil
}
