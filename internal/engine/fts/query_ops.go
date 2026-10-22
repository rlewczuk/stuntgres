package fts

import (
	"context"
	"fmt"
)

func (q *Query) And(ctx context.Context, other *Query) (*Query, error) {
	return q.compose(ctx, other, '&', 1)
}
func (q *Query) Or(ctx context.Context, other *Query) (*Query, error) {
	return q.compose(ctx, other, '|', 1)
}
func (q *Query) Not(ctx context.Context) (*Query, error) { return q.compose(ctx, nil, '!', 1) }
func (q *Query) Phrase(ctx context.Context, other *Query, distance int32) (*Query, error) {
	return q.compose(ctx, other, '<', distance)
}

func (q *Query) compose(ctx context.Context, other *Query, op byte, distance int32) (*Query, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !q.Valid() || op != '!' && !other.Valid() {
		return nil, fmt.Errorf("invalid tsquery operands")
	}
	if distance < 0 || distance > 16384 {
		return nil, failure("22023", "distance in phrase operator must be an integer value between zero and 16384 inclusive")
	}
	if op == '!' && q.Empty() {
		return q, ctx.Err()
	}
	if op != '!' {
		if q.Empty() {
			return other, ctx.Err()
		}
		if other.Empty() {
			return q, ctx.Err()
		}
	}
	count := uint64(len(q.nodes)) + 1
	if other != nil {
		count += uint64(len(other.nodes))
	}
	// Arenas are postorder, left before right. Reverse scanning value nodes is
	// exactly released right-first operand serialization, without an AST scratch.
	var bytes uint64
	for _, operand := range [2]*Query{other, q} {
		if operand == nil {
			continue
		}
		for i := len(operand.nodes) - 1; i >= 0; i-- {
			if i&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			n := operand.nodes[i]
			if n.op != 'v' {
				continue
			}
			if bytes > maxStringOffset {
				return nil, failure("54000", "tsquery is too large")
			}
			bytes += uint64(len(n.word)) + 1
		}
	}
	if queryTooBig(count, bytes) {
		return nil, failure("54000", "tsquery is too large")
	}
	nodes := make([]queryNode, int(count))
	copyOperand := func(operand *Query, offset int) error {
		for i, n := range operand.nodes {
			if i&255 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if n.op != 'v' {
				n.right += offset
				if n.op != '!' {
					n.left += offset
				}
			}
			nodes[offset+i] = n
		}
		return nil
	}
	if err := copyOperand(q, 0); err != nil {
		return nil, err
	}
	root := queryNode{op: op, right: q.root}
	if op == '<' {
		root.distance = uint16(distance)
	}
	if other != nil {
		offset := len(q.nodes)
		if err := copyOperand(other, offset); err != nil {
			return nil, err
		}
		root.left = q.root
		root.right = other.root + offset
	}
	nodes[len(nodes)-1] = root
	result := &Query{nodes: nodes, root: len(nodes) - 1}
	text, err := result.render(ctx, false)
	if err != nil {
		return nil, err
	}
	result.text = text
	return result, ctx.Err()
}

// Contains compares exact lexeme sets, not Boolean meaning, prefix matching,
// weights, negation or multiplicity. Immutable payloads never retain this set.
func (q *Query) Contains(ctx context.Context, other *Query) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !q.Valid() || !other.Valid() {
		return false, fmt.Errorf("invalid tsquery operands")
	}
	if other.Empty() {
		return true, ctx.Err()
	}
	if q.Empty() {
		return false, ctx.Err()
	}
	words := make(map[string]struct{})
	for i, n := range q.nodes {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if n.op == 'v' {
			words[n.word] = struct{}{}
		}
	}
	for i, n := range other.nodes {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if n.op == 'v' {
			if _, ok := words[n.word]; !ok {
				return false, ctx.Err()
			}
		}
	}
	return true, ctx.Err()
}
