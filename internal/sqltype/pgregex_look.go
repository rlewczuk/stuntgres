package sqltype

import "unicode/utf8"

// Assertion evaluation uses one checked heap continuation stack. No nesting
// level calls the Go evaluator recursively; parent and child scratch coexist.
type pgRegexLookState struct{ pc, pos int }
type pgRegexLookFrame struct {
	base, scan, end, continuation, pos int
	negative, behind                   bool
}

func (p *PGRegexProgram) pushLook(w *pgRegexWork, source string, i pgRegexInst, pos int) error {
	f := pgRegexLookFrame{base: len(p.lookScratch), scan: len(p.lookScratch), end: pos, pos: pos, continuation: i.x, negative: i.value == '!' || i.value == '!'+256, behind: i.value >= 256}
	if err := pgRegexPush(&p.budget, &p.lookFrames, f); err != nil {
		return err
	}
	if !f.behind {
		return pgRegexPush(&p.budget, &p.lookScratch, pgRegexLookState{i.y, pos})
	}
	for at := 0; ; {
		if err := w.step(); err != nil {
			return err
		}
		if err := pgRegexPush(&p.budget, &p.lookScratch, pgRegexLookState{i.y, at}); err != nil {
			return err
		}
		if at == pos {
			break
		}
		_, width := utf8.DecodeRuneInString(source[at:pos])
		at += width
	}
	return nil
}
func (p *PGRegexProgram) lookInsert(w *pgRegexWork, f pgRegexLookFrame, state pgRegexLookState) error {
	for at := f.base; at < len(p.lookScratch); at++ {
		if err := w.step(); err != nil {
			return err
		}
		if p.lookScratch[at] == state {
			return nil
		}
	}
	return pgRegexPush(&p.budget, &p.lookScratch, state)
}
func (p *PGRegexProgram) evaluateLook(w *pgRegexWork, source string, i pgRegexInst, pos int) (bool, error) {
	p.lookScratch = p.lookScratch[:0]
	p.lookFrames = p.lookFrames[:0]
	if err := p.pushLook(w, source, i, pos); err != nil {
		return false, err
	}
	for len(p.lookFrames) > 0 {
		if err := w.step(); err != nil {
			return false, err
		}
		top := len(p.lookFrames) - 1
		f := p.lookFrames[top]
		finished, matched := f.scan == len(p.lookScratch), false
		if !finished {
			state := p.lookScratch[f.scan]
			p.lookFrames[top].scan++
			inst := p.code[state.pc]
			if inst.op == pgRegexAccept {
				matched = !f.behind || state.pos == f.end
				finished = matched
			} else if inst.op == pgRegexLook {
				// Save the consuming parent's next PC in the child continuation.
				if err := p.pushLook(w, source, inst, state.pos); err != nil {
					return false, err
				}
				continue
			} else {
				nextpos, yes := state.pos, true
				switch inst.op {
				case pgRegexSplit:
					if err := p.lookInsert(w, f, pgRegexLookState{inst.y, state.pos}); err != nil {
						return false, err
					}
				case pgRegexTag, pgRegexReset, pgRegexProgressBegin, pgRegexProgressEnd:
				case pgRegexBegin, pgRegexEnd, pgRegexAbsoluteBegin, pgRegexAbsoluteEnd, pgRegexWordBegin, pgRegexWordEnd, pgRegexWordBoundary, pgRegexNotWordBoundary:
					yes = p.assertion(inst.op, source, state.pos)
				default:
					if state.pos == len(source) || f.behind && state.pos == f.end {
						yes = false
					} else {
						r, n := utf8.DecodeRuneInString(source[state.pos:])
						nextpos += n
						var err error
						yes, err = p.matchCharacter(w, inst, r)
						if err != nil {
							return false, err
						}
					}
				}
				if yes {
					if err := p.lookInsert(w, f, pgRegexLookState{inst.x, nextpos}); err != nil {
						return false, err
					}
				}
			}
		}
		if !finished {
			continue
		}
		result := matched != f.negative
		p.lookScratch = p.lookScratch[:f.base]
		p.lookFrames = p.lookFrames[:top]
		if top == 0 {
			return result, w.ctx.Err()
		}
		if result {
			parent := p.lookFrames[top-1]
			if err := p.lookInsert(w, parent, pgRegexLookState{f.continuation, f.pos}); err != nil {
				return false, err
			}
		}
	}
	return false, w.ctx.Err()
}
