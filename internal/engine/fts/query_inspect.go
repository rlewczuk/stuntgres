package fts

import "context"

// NodeCount includes every operand occurrence and every operator.
func (q *Query) NodeCount() int32 { return int32(len(q.nodes)) }

// IndexableTree removes entire NOT subtrees, treating them as TRUE. This is
// index qualification, not Boolean simplification (even double NOT is TRUE).
func (q *Query) IndexableTree(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if q.Empty() {
		return "", nil
	}
	negated := false
	for i, n := range q.nodes {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if n.op == '!' {
			negated = true
			break
		}
	}
	if !negated {
		return q.text, nil
	}
	// -1 is TRUE; all other entries index the reduced postorder arena.
	mapped := make([]int, len(q.nodes))
	nodes := make([]queryNode, 0, len(q.nodes))
	for i, n := range q.nodes {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		switch n.op {
		case '!':
			mapped[i] = -1
			continue
		case '&', '|', '<':
			a, b := mapped[n.left], mapped[n.right]
			if n.op == '|' && (a == -1 || b == -1) {
				mapped[i] = -1
				continue
			}
			if a == -1 {
				mapped[i] = b
				continue
			}
			if b == -1 {
				mapped[i] = a
				continue
			}
			n.left, n.right = a, b
		}
		mapped[i] = len(nodes)
		nodes = append(nodes, n)
	}
	if mapped[q.root] == -1 {
		return "T", ctx.Err()
	}
	// Collapsing an operator can select an older arena node. Later entries
	// belong to discarded branches; retain the root's postorder prefix so the
	// renderer's root-last invariant holds without copying surviving nodes.
	root := mapped[q.root]
	return (&Query{nodes: nodes[:root+1], root: root}).render(ctx, false)
}
