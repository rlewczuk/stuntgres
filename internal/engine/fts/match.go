package fts

import (
	"cmp"
	"context"
	"fmt"
	"strings"
)

const (
	matchNo byte = iota
	matchYes
	matchMaybe
)

// A positional result describes match ends, not starts. A complement denotes
// all locations except these ends; it never requires enumerating a universe.
// Unfiltered leaf positions borrow immutable vector storage.
type matchResult struct {
	state      byte
	width      int
	complement bool
	view       []position
	ends       []int
}

func (r matchResult) len() int {
	if r.view != nil {
		return len(r.view)
	}
	return len(r.ends)
}
func (r matchResult) at(i int) int {
	if r.view != nil {
		return int(r.view[i].number & 0x3fff)
	}
	// ExecPhraseData stores generated ends in WordEntryPos. Each parent
	// consumes WEP_GETPOS, independently of the child's wide phrase width.
	return r.ends[i] & 0x3fff
}
func matchTruth(ok bool) matchResult {
	if ok {
		return matchResult{state: matchYes}
	}
	return matchResult{}
}

// Match reads the immutable query arena and vector directly. Boolean evaluation
// short-circuits; only a phrase demands locations throughout its subtree.
func (v *Vector) Match(ctx context.Context, q *Query) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !v.Valid() || !q.Valid() {
		return false, fmt.Errorf("invalid FTS match operand")
	}
	if q.Empty() {
		return false, nil
	}
	type frame struct {
		index      int
		phase      byte
		positional bool
		left       matchResult
	}
	var inline [32]frame
	stack := append(inline[:0], frame{index: q.root})
	var result matchResult
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		f := &stack[len(stack)-1]
		n := q.nodes[f.index]
		positional := f.positional || n.op == '<'
		if n.op == 'v' {
			var err error
			result, err = v.matchLeaf(ctx, n, positional)
			if err != nil {
				return false, err
			}
		} else {
			if n.op != '!' && n.op != '&' && n.op != '|' && n.op != '<' ||
				n.op != '!' && (n.left < 0 || n.left >= f.index) ||
				n.right < 0 || n.right >= f.index {
				return false, fmt.Errorf("invalid FTS match query arena")
			}
			if f.phase == 0 {
				f.phase = 1
				child := n.left
				if n.op == '!' {
					child = n.right
				}
				stack = append(stack, frame{index: child, positional: positional})
				continue
			}
			if n.op == '!' {
				if !positional {
					result = matchTruth(result.state == matchNo)
				} else if result.state != matchMaybe {
					if result.state == matchNo {
						result.state, result.complement = matchYes, true
					} else {
						result.complement = !result.complement
						if !result.complement && result.len() == 0 {
							result.state = matchNo
						}
					}
				}
			} else {
				if f.phase == 1 {
					f.left = result
					if !positional && (n.op == '&' && result.state == matchNo || n.op == '|' && result.state == matchYes) || positional && n.op != '|' && result.state == matchNo {
						// The left result already determines this subtree.
						if positional {
							// The parent has not calculated its own width.
							result = matchResult{}
						}
					} else {
						f.phase = 2
						stack = append(stack, frame{index: n.right, positional: positional})
						continue
					}
				} else if !positional {
					if n.op == '&' {
						result = matchTruth(f.left.state == matchYes && result.state == matchYes)
					} else {
						result = matchTruth(f.left.state == matchYes || result.state == matchYes)
					}
				} else {
					var err error
					result, err = matchCombine(ctx, n.op, int(n.distance), f.left, result)
					if err != nil {
						return false, err
					}
				}
			}
		}
		// MAYBE becomes false at the phrase boundary, before document NOT.
		if !f.positional && n.op == '<' {
			result = matchTruth(result.state == matchYes)
		}
		stack = stack[:len(stack)-1]
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return result.state == matchYes, nil
}

func (v *Vector) matchLeaf(ctx context.Context, n queryNode, positional bool) (matchResult, error) {
	lo, hi := 0, len(v.lexemes)
	for lo < hi {
		if err := ctx.Err(); err != nil {
			return matchResult{}, err
		}
		mid := lo + (hi-lo)/2
		if v.lexemes[mid].word < n.word {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	var out matchResult
	for i := lo; i < len(v.lexemes); i++ {
		if err := ctx.Err(); err != nil {
			return matchResult{}, err
		}
		entry := v.lexemes[i]
		if entry.word != n.word && (!n.prefix || !strings.HasPrefix(entry.word, n.word)) {
			break
		}
		if len(entry.positions) == 0 {
			if positional {
				return matchResult{state: matchMaybe}, nil
			}
			return matchTruth(true), nil
		}
		if positional && !n.prefix && n.mask == 0 {
			return matchResult{state: matchYes, view: entry.positions}, nil
		}
		for j, p := range entry.positions {
			if j&255 == 0 {
				if err := ctx.Err(); err != nil {
					return matchResult{}, err
				}
			}
			if n.mask == 0 || n.mask&(1<<p.weight) != 0 {
				if !positional {
					return matchTruth(true), nil
				}
				out.ends = append(out.ends, int(p.number))
			}
		}
		if !n.prefix {
			break
		}
	}
	if len(out.ends) > 0 {
		if n.prefix {
			if err := stableSort(ctx, out.ends, cmp.Compare[int]); err != nil {
				return matchResult{}, err
			}
			count := 0
			for i, end := range out.ends {
				if i&255 == 0 {
					if err := ctx.Err(); err != nil {
						return matchResult{}, err
					}
				}
				if count == 0 || out.ends[count-1] != end {
					out.ends[count] = end
					count++
				}
			}
			out.ends = out.ends[:count]
		}
		out.state = matchYes
	}
	return out, nil
}

func matchCombine(ctx context.Context, op byte, distance int, a, b matchResult) (matchResult, error) {
	if err := ctx.Err(); err != nil {
		return matchResult{}, err
	}
	if op != '|' && (a.state == matchNo || b.state == matchNo) {
		return matchResult{}, nil
	}
	if a.state == matchMaybe || b.state == matchMaybe {
		return matchResult{state: matchMaybe}, nil
	}
	if a.state == matchNo && b.state == matchNo {
		return matchResult{}, nil
	}
	if op == '|' {
		// Failed OR children have no width for alignment, even when a
		// failed positional intersection had already calculated one.
		if a.state == matchNo {
			a.width = 0
		}
		if b.state == matchNo {
			b.width = 0
		}
	}
	width := max(a.width, b.width)
	leftShift, rightShift := width-a.width, width-b.width
	if op == '<' {
		width = a.width + distance + b.width
		leftShift, rightShift = distance+b.width, 0
	}
	// Select a finite-set merge from the complement truth table. Difference
	// always retains the named side, with shifts applied only while reading.
	const (
		intersection = iota
		union
		leftDifference
		rightDifference
	)
	mode, complement := intersection, false
	if op == '|' {
		switch {
		case a.complement && b.complement:
			mode, complement = intersection, true
		case a.complement:
			mode, complement = leftDifference, true
		case b.complement:
			mode, complement = rightDifference, true
		default:
			mode = union
		}
	} else {
		switch {
		case a.complement && b.complement:
			mode, complement = union, true
		case a.complement:
			mode = rightDifference
		case b.complement:
			mode = leftDifference
		}
	}
	capacity := min(a.len(), b.len())
	switch mode {
	case union:
		capacity = a.len() + b.len()
	case leftDifference:
		capacity = a.len()
	case rightDifference:
		capacity = b.len()
	}
	out := matchResult{state: matchYes, width: width, complement: complement}
	for i, j, iterations := 0, 0, 0; i < a.len() || j < b.len(); iterations++ {
		if mode == intersection && (i == a.len() || j == b.len()) ||
			mode == leftDifference && i == a.len() ||
			mode == rightDifference && j == b.len() {
			break
		}
		if iterations&255 == 0 {
			if err := ctx.Err(); err != nil {
				return matchResult{}, err
			}
		}
		var x, y int
		if i < a.len() {
			x = a.at(i) + leftShift
		}
		if j < b.len() {
			y = b.at(j) + rightShift
		}
		emit, end := false, 0
		switch {
		case i == a.len() || j < b.len() && y < x:
			emit, end = mode == union || mode == rightDifference, y
			j++
		case j == b.len() || x < y:
			emit, end = mode == union || mode == leftDifference, x
			i++
		default:
			emit, end = mode == intersection || mode == union, x
			i++
			j++
		}
		if emit && end > 0 {
			if out.ends == nil {
				out.ends = make([]int, 0, capacity)
			}
			out.ends = append(out.ends, int(uint16(end)))
		}
	}
	if err := ctx.Err(); err != nil {
		return matchResult{}, err
	}
	if !out.complement && out.len() == 0 {
		return matchResult{width: width}, nil
	}
	return out, nil
}
