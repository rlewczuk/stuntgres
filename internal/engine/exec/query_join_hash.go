package exec

import (
	"context"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// joinHash indexes the right input of a join whose whole predicate is a
// conjunction of builtin same-type equalities, each between a key of the left
// row and a key of the right row. A key is a plain column of one side, maybe
// cast to oid from another OID type. The first left row still scans every
// right row, so right production and its failures keep their order. Then the
// index is built, and each later left row visits only the right rows whose key
// hash matches, in ascending order, and still evaluates the whole predicate on
// them: a skipped pair has a NULL or unequal key, so its predicate is not TRUE.
// A right key that fails leaves the join on the full scan; a left key that
// fails scans every right row for that left row.
type joinHash struct {
	left, right []expr.Expr
	groupings   []sqltype.Grouping
	buckets     map[uint64][]int // right occurrences, ascending
	ready       bool
}

// newJoinHash returns nil when the predicate does not have that shape.
func newJoinHash(n *plan.JoinNode, evaluation *Context) *joinHash {
	if n.Predicate == nil || n.CorrelatedRight {
		return nil
	}
	h := &joinHash{}
	if !h.add(n, evaluation, n.Predicate) {
		return nil
	}
	return h
}

func (h *joinHash) add(n *plan.JoinNode, evaluation *Context, e expr.Expr) bool {
	switch c := e.(type) {
	case plan.LogicalExpr:
		return c.Op == parser.LogicalAnd && !c.QueryRight && c.Right != nil && h.add(n, evaluation, c.Left) && h.add(n, evaluation, c.Right)
	case plan.ComparisonExpr:
		if c.Operator != "=" || c.RoutineRef != (ids.ObjectRef{}) || c.Namespace != "" && c.Namespace != "pg_catalog" || c.Properties().Volatility == expr.Volatile {
			return false
		}
		left, right := c.Left, c.Right
		if !joinKey(evaluation, left, 0, n.LeftWidth) || !joinKey(evaluation, right, n.LeftWidth, n.RightWidth) {
			left, right = right, left
			if !joinKey(evaluation, left, 0, n.LeftWidth) || !joinKey(evaluation, right, n.LeftWidth, n.RightWidth) {
				return false
			}
		}
		grouping, err := c.Predicate.EqualityGrouping()
		if err != nil {
			return false
		}
		h.left, h.right = append(h.left, left), append(h.right, right)
		h.groupings = append(h.groupings, grouping)
		return true
	}
	return false
}

// joinKey accepts a plain column of the join's own frame in [start,
// start+width), maybe under casts to oid from OID types, which only relabel it.
func joinKey(evaluation *Context, e expr.Expr, start, width int) bool {
	for {
		coercion, ok := e.(plan.CoercionExpr)
		if !ok {
			break
		}
		if !oidKeyType(coercion.ResultUsage().Type) || !oidKeyType(coercion.Child.ResultUsage().Type) {
			return false
		}
		e = coercion.Child
	}
	c, ok := e.(plan.ColumnExpr)
	return ok && c.Virtual == nil && !c.MergeSource && c.Scope == 1 && c.Frame == evaluation.Frame && c.Slot >= start && c.Slot < start+width
}

func oidKeyType(t *sqltype.Descriptor) bool {
	if t == nil {
		return false
	}
	id := t.ID()
	return id == sqltype.Oid || id == sqltype.Regclass || id == sqltype.Regtype || id == sqltype.Regrole ||
		sqltype.IsOperatorAlias(id) || sqltype.IsRoutineAlias(id) || sqltype.IsRegObjectAlias(id) || sqltype.IsRegTS(id)
}

// key hashes keys evaluated over evaluation's frame. null reports a NULL key.
func (h *joinHash) key(evaluation *Context, keys []expr.Expr) (hash uint64, null bool, err error) {
	hash = 14695981039346656037
	for i, key := range keys {
		value, err := key.Eval(evaluation)
		if err != nil {
			return 0, false, err
		}
		value, err = h.groupings[i].PrepareKey(evaluation.Context, value)
		if err != nil {
			return 0, false, err
		}
		if value.IsNull() {
			return 0, true, nil
		}
		part, err := h.groupings[i].Hash(evaluation.Context, value)
		if err != nil {
			return 0, false, err
		}
		hash = (hash ^ part) * 1099511628211
	}
	return hash, false, nil
}

// build indexes the count right occurrences the first left row produced,
// copying each into the right part of frame. It reports false, leaving the
// join on the full scan, when a key fails.
func (h *joinHash) build(ctx context.Context, evaluation *Context, frame []sqltype.Value, leftWidth int, right *Stage, count int) (bool, error) {
	evaluation.setContext(ctx)
	h.buckets = make(map[uint64][]int)
	for j := range count {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		copy(frame[leftWidth:], right.Source.Row(right.Index(j)))
		hash, null, err := h.key(evaluation, h.right)
		if err != nil {
			h.buckets = nil
			return false, nil
		}
		if !null {
			h.buckets[hash] = append(h.buckets[hash], j)
		}
	}
	h.ready = true
	return true, nil
}

// candidates returns the right occurrences the left row in evaluation's frame
// can match; scan reports a failed key, so every right row must be visited.
func (h *joinHash) candidates(ctx context.Context, evaluation *Context) (matches []int, scan bool) {
	evaluation.setContext(ctx)
	hash, null, err := h.key(evaluation, h.left)
	if err != nil {
		return nil, true
	}
	if null {
		return nil, false
	}
	return h.buckets[hash], false
}
