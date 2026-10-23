package fts

import (
	"context"
	"hash/crc32"
	"strings"
)

type rewriteNode struct {
	value    queryNode
	children []int // PostgreSQL storage order: right child first.
	crc      int32
	noChange bool
}

type rewritePair struct {
	a, b     int
	distance bool
}
type rewriteVisit struct {
	node int
	exit bool
}

// RewriteWorkspace owns transient nodes only. Immutable input words may be
// shared; caller-visible query arenas and their cached text are never changed.
// A SQL rule stream reuses this arena and emits one final immutable query.
type RewriteWorkspace struct {
	ctx                         context.Context
	nodes                       []rewriteNode
	edges                       []int
	root                        int
	work                        uint32
	compare                     []rewritePair
	merge                       []int
	visits                      []rewriteVisit
	pending, flattened, matched []int
}

var rewriteCRCTable = crc32.MakeTable(crc32.IEEE)

func (w *RewriteWorkspace) checkpoint() error {
	w.work++
	if w.work&255 == 0 {
		return w.ctx.Err()
	}
	return nil
}

func (w *RewriteWorkspace) children(values ...int) []int {
	start := len(w.edges)
	w.edges = append(w.edges, values...)
	return w.edges[start:len(w.edges):len(w.edges)]
}

func (w *RewriteWorkspace) importQuery(q *Query) (int, error) {
	if q.Empty() {
		return -1, w.ctx.Err()
	}
	offset := len(w.nodes)
	for _, value := range q.nodes {
		if err := w.checkpoint(); err != nil {
			return -1, err
		}
		n := rewriteNode{value: value}
		if value.op == 'v' {
			crc := uint32(0xffffffff)
			for i := range len(value.word) {
				if err := w.checkpoint(); err != nil {
					return -1, err
				}
				crc = rewriteCRCTable[byte(crc>>24)^value.word[i]] ^ (crc << 8)
			}
			n.crc = int32(crc ^ 0xffffffff)
		} else if value.op == '!' {
			n.children = w.children(value.right + offset)
		} else {
			n.children = w.children(value.right+offset, value.left+offset)
		}
		w.nodes = append(w.nodes, n)
	}
	return q.root + offset, w.ctx.Err()
}

func rewriteRank(op byte) int {
	switch op {
	case '!':
		return 1
	case '&':
		return 2
	case '|':
		return 3
	case '<':
		return 4
	}
	return 0
}

func (w *RewriteWorkspace) cmp(a, b int) (int, error) {
	w.compare = append(w.compare[:0], rewritePair{a: a, b: b})
	for len(w.compare) > 0 {
		if err := w.checkpoint(); err != nil {
			return 0, err
		}
		p := w.compare[len(w.compare)-1]
		w.compare = w.compare[:len(w.compare)-1]
		x, y := w.nodes[p.a], w.nodes[p.b]
		if p.distance {
			if x.value.distance > y.value.distance {
				return -1, nil
			}
			if x.value.distance < y.value.distance {
				return 1, nil
			}
			continue
		}
		xr, yr := rewriteRank(x.value.op), rewriteRank(y.value.op)
		if xr > yr {
			return -1, nil
		}
		if xr < yr {
			return 1, nil
		}
		if xr == 0 {
			if x.crc > y.crc {
				return -1, nil
			}
			if x.crc < y.crc {
				return 1, nil
			}
			// Compare in bounded chunks so a CRC collision on long words is
			// cancellable too, rather than hiding work inside strings.Compare.
			left, right := x.value.word, y.value.word
			for len(left) > 0 && len(right) > 0 {
				if err := w.checkpoint(); err != nil {
					return 0, err
				}
				n := min(256, len(left), len(right))
				if c := strings.Compare(left[:n], right[:n]); c != 0 {
					return c, nil
				}
				left, right = left[n:], right[n:]
			}
			if len(left) < len(right) {
				return -1, nil
			}
			if len(left) > len(right) {
				return 1, nil
			}
			continue
		}
		if len(x.children) > len(y.children) {
			return -1, nil
		}
		if len(x.children) < len(y.children) {
			return 1, nil
		}
		if x.value.op == '<' {
			w.compare = append(w.compare, rewritePair{a: p.a, b: p.b, distance: true})
		}
		for i := len(x.children) - 1; i >= 0; i-- {
			if err := w.checkpoint(); err != nil {
				return 0, err
			}
			w.compare = append(w.compare, rewritePair{a: x.children[i], b: y.children[i]})
		}
	}
	return 0, nil
}

func (w *RewriteWorkspace) sortChildren(root int) error {
	children := w.nodes[root].children
	if len(children) < 2 || w.nodes[root].value.op == '<' {
		return nil
	}
	ordered := true
	for i := 1; i < len(children); i++ {
		c, err := w.cmp(children[i-1], children[i])
		if err != nil {
			return err
		}
		if c > 0 {
			ordered = false
			break
		}
	}
	if ordered {
		return nil
	}
	if cap(w.merge) < len(children) {
		w.merge = make([]int, len(children))
	}
	scratch := w.merge[:len(children)]
	for width := 1; width < len(children); width *= 2 {
		for start := 0; start < len(children); start += 2 * width {
			middle, end := min(start+width, len(children)), min(start+2*width, len(children))
			a, b := start, middle
			for k := start; k < end; k++ {
				if err := w.checkpoint(); err != nil {
					return err
				}
				chooseA := b == end
				if !chooseA && a < middle {
					c, err := w.cmp(children[a], children[b])
					if err != nil {
						return err
					}
					chooseA = c <= 0
				}
				if chooseA {
					scratch[k] = children[a]
					a++
				} else {
					scratch[k] = children[b]
					b++
				}
			}
		}
		for i := range children {
			if err := w.checkpoint(); err != nil {
				return err
			}
			children[i] = scratch[i]
		}
	}
	return nil
}

func (w *RewriteWorkspace) normalize(root int) error {
	if root < 0 {
		return w.ctx.Err()
	}
	stack := append(w.visits[:0], rewriteVisit{node: root})
	pending, flattened := w.pending[:0], w.flattened[:0]
	defer func() { w.visits, w.pending, w.flattened = stack[:0], pending[:0], flattened[:0] }()
	for len(stack) > 0 {
		if err := w.checkpoint(); err != nil {
			return err
		}
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if v.exit {
			if err := w.sortChildren(v.node); err != nil {
				return err
			}
			continue
		}
		n := w.nodes[v.node]
		w.nodes[v.node].noChange = false
		if n.value.op == '&' || n.value.op == '|' {
			pending, flattened = pending[:0], flattened[:0]
			changed := false
			for i := len(n.children) - 1; i >= 0; i-- {
				pending = append(pending, n.children[i])
			}
			for len(pending) > 0 {
				if err := w.checkpoint(); err != nil {
					return err
				}
				child := pending[len(pending)-1]
				pending = pending[:len(pending)-1]
				if w.nodes[child].value.op == n.value.op {
					changed = true
					for i := len(w.nodes[child].children) - 1; i >= 0; i-- {
						if err := w.checkpoint(); err != nil {
							return err
						}
						pending = append(pending, w.nodes[child].children[i])
					}
				} else {
					flattened = append(flattened, child)
				}
			}
			if changed {
				w.nodes[v.node].children = w.children(flattened...)
			}
		}
		stack = append(stack, rewriteVisit{node: v.node, exit: true})
		for _, child := range w.nodes[v.node].children {
			if err := w.checkpoint(); err != nil {
				return err
			}
			stack = append(stack, rewriteVisit{node: child})
		}
	}
	return w.ctx.Err()
}
