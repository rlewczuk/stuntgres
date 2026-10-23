package fts

import (
	"context"
	"fmt"
)

func NewRewriteWorkspace(ctx context.Context, q *Query) (*RewriteWorkspace, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !q.Valid() {
		return nil, fmt.Errorf("invalid tsquery rewrite input")
	}
	w := &RewriteWorkspace{ctx: ctx, root: -1, nodes: make([]rewriteNode, 0, len(q.nodes)), edges: make([]int, 0, len(q.nodes))}
	root, err := w.importQuery(q)
	if err != nil {
		return nil, err
	}
	w.root = root
	if err := w.normalize(root); err != nil {
		return nil, err
	}
	return w, nil
}

// Empty reports current accumulator state without reconstruction or rendering.
func (w *RewriteWorkspace) Empty() bool { return w.root < 0 }

func (q *Query) Rewrite(ctx context.Context, target, substitute *Query) (*Query, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !q.Valid() || !target.Valid() || !substitute.Valid() {
		return nil, fmt.Errorf("invalid tsquery rewrite operands")
	}
	if q.Empty() || target.Empty() {
		return q, nil
	}
	w, err := NewRewriteWorkspace(ctx, q)
	if err != nil {
		return nil, err
	}
	if err := w.apply(target, substitute); err != nil {
		return nil, err
	}
	return w.Finish()
}

// ApplyRule clears inserted-node protection only at SQL row boundaries. A rule
// never recursively matches its own replacement; a later rule may match it.
func (w *RewriteWorkspace) ApplyRule(target, substitute *Query) error {
	if !target.Valid() || !substitute.Valid() {
		return fmt.Errorf("invalid tsquery rewrite operands")
	}
	if target.Empty() || w.root < 0 {
		return w.ctx.Err()
	}
	if err := w.apply(target, substitute); err != nil {
		return err
	}
	return w.normalize(w.root)
}

func (w *RewriteWorkspace) sortTree(root int) error {
	stack := []rewriteVisit{{node: root}}
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
		stack = append(stack, rewriteVisit{node: v.node, exit: true})
		for _, child := range w.nodes[v.node].children {
			if err := w.checkpoint(); err != nil {
				return err
			}
			stack = append(stack, rewriteVisit{node: child})
		}
	}
	return nil
}

func (w *RewriteWorkspace) replacement(q *Query) (int, error) {
	root, err := w.importQuery(q)
	if err == nil && root >= 0 {
		w.nodes[root].noChange = true
	}
	return root, err
}

func (w *RewriteWorkspace) match(root, target int, substitute *Query) (int, error) {
	n, ex := w.nodes[root], w.nodes[target]
	if n.noChange || n.value.op != ex.value.op {
		return root, nil
	}
	if len(n.children) == len(ex.children) {
		c, err := w.cmp(root, target)
		if err != nil {
			return -1, err
		}
		if c == 0 {
			return w.replacement(substitute)
		}
	} else if len(n.children) > len(ex.children) && len(ex.children) > 0 && (n.value.op == '&' || n.value.op == '|') {
		// Sorted multiset matching consumes a distinct occurrence per target.
		matched := w.matched[:0]
		defer func() { w.matched = matched[:0] }()
		i, j := 0, 0
		for i < len(n.children) && j < len(ex.children) {
			if err := w.checkpoint(); err != nil {
				return -1, err
			}
			c, err := w.cmp(n.children[i], ex.children[j])
			if err != nil {
				return -1, err
			}
			if c == 0 {
				matched = append(matched, i)
				i++
				j++
			} else if c < 0 {
				i++
			} else {
				break
			}
		}
		if j == len(ex.children) {
			out, next := 0, 0
			for i, child := range n.children {
				if err := w.checkpoint(); err != nil {
					return -1, err
				}
				if next < len(matched) && matched[next] == i {
					next++
					continue
				}
				n.children[out] = child
				out++
			}
			r, err := w.replacement(substitute)
			if err != nil {
				return -1, err
			}
			if r >= 0 {
				n.children[out] = r
				out++
			}
			w.nodes[root].children = n.children[:out]
			// Released subset replacement sorts descendants, including the new
			// substitute, but does not flatten away NOCHANGE boundaries.
			if err := w.sortTree(root); err != nil {
				return -1, err
			}
		}
	}
	return root, nil
}

func (w *RewriteWorkspace) apply(target, substitute *Query) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	ex, err := w.importQuery(target)
	if err != nil {
		return err
	}
	if err := w.normalize(ex); err != nil {
		return err
	}
	if w.root < 0 || ex < 0 {
		return nil
	}
	type frame struct {
		node, parent, slot int
		exit               bool
	}
	stack := []frame{{node: w.root, parent: -1}}
	for len(stack) > 0 {
		if err := w.checkpoint(); err != nil {
			return err
		}
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		root := f.node
		if !f.exit {
			root, err = w.match(root, ex, substitute)
			if err != nil {
				return err
			}
			if root >= 0 && !w.nodes[root].noChange && len(w.nodes[root].children) > 0 {
				stack = append(stack, frame{node: root, parent: f.parent, slot: f.slot, exit: true})
				children := w.nodes[root].children
				for i := len(children) - 1; i >= 0; i-- {
					if err := w.checkpoint(); err != nil {
						return err
					}
					stack = append(stack, frame{node: children[i], parent: root, slot: i})
				}
				continue
			}
		} else {
			n := &w.nodes[root]
			out := 0
			for _, child := range n.children {
				if err := w.checkpoint(); err != nil {
					return err
				}
				if child >= 0 {
					n.children[out] = child
					out++
				}
			}
			n.children = n.children[:out]
			if out == 0 {
				root = -1
			} else if out == 1 && n.value.op != '!' {
				root = n.children[0]
			}
		}
		if f.parent < 0 {
			w.root = root
		} else {
			w.nodes[f.parent].children[f.slot] = root
		}
	}
	return w.ctx.Err()
}

// Finish performs released QTNBinary grouping, then left-first postorder arena
// emission. It does not parse rendered text, including generated deep NOT trees.
func (w *RewriteWorkspace) Finish() (*Query, error) {
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	if w.root < 0 {
		return &Query{root: -1}, nil
	}
	stack := []rewriteVisit{{node: w.root}}
	for len(stack) > 0 {
		if err := w.checkpoint(); err != nil {
			return nil, err
		}
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !v.exit {
			stack = append(stack, rewriteVisit{node: v.node, exit: true})
			for _, child := range w.nodes[v.node].children {
				if err := w.checkpoint(); err != nil {
					return nil, err
				}
				stack = append(stack, rewriteVisit{node: child})
			}
			continue
		}
		children := w.nodes[v.node].children
		for len(children) > 2 {
			if err := w.checkpoint(); err != nil {
				return nil, err
			}
			value := w.nodes[v.node].value
			index := len(w.nodes)
			w.nodes = append(w.nodes, rewriteNode{value: value, children: w.children(children[0], children[1])})
			children[0], children[1] = index, children[len(children)-1]
			children = children[:len(children)-1]
		}
		w.nodes[v.node].children = children
	}
	// Released operand offsets are assigned right-first, not in output arena
	// order. Check exact reachable capacity before allocating the final arena.
	var count, bytes uint64
	pending := []int{w.root}
	for len(pending) > 0 {
		if err := w.checkpoint(); err != nil {
			return nil, err
		}
		index := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		n := w.nodes[index]
		count++
		if n.value.op == 'v' {
			if bytes > maxStringOffset {
				return nil, failure("54000", "tsquery is too large")
			}
			bytes += uint64(len(n.value.word)) + 1
		}
		if queryTooBig(count, bytes) {
			return nil, failure("54000", "tsquery is too large")
		}
		for i := len(n.children) - 1; i >= 0; i-- {
			pending = append(pending, n.children[i])
		}
	}
	mapped := make([]int, len(w.nodes))
	nodes := make([]queryNode, 0, int(count))
	stack = append(stack[:0], rewriteVisit{node: w.root})
	for len(stack) > 0 {
		if err := w.checkpoint(); err != nil {
			return nil, err
		}
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := w.nodes[v.node]
		if !v.exit {
			stack = append(stack, rewriteVisit{node: v.node, exit: true})
			for _, child := range n.children {
				stack = append(stack, rewriteVisit{node: child})
			}
			continue
		}
		value := n.value
		if value.op != 'v' {
			value.right = mapped[n.children[0]]
			if len(n.children) > 1 {
				value.left = mapped[n.children[1]]
			}
		}
		mapped[v.node] = len(nodes)
		nodes = append(nodes, value)
	}
	q := &Query{nodes: nodes, root: len(nodes) - 1}
	text, err := q.render(w.ctx, false)
	if err != nil {
		return nil, err
	}
	q.text = text
	return q, w.ctx.Err()
}
