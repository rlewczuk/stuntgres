package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type wholeRowSource struct {
	namespace, qualifier int
	frame                uint32
}

// A retained named ROW keeps written children for metadata and dependencies.
// Only a real shape change installs this immutable stable-attribute projection.
type rowFieldProjection struct {
	attribute int32
	slot      int // Current live member, or -1 when that original attribute retired.
}

type rowExpression struct {
	expr.Info
	fields          []expr.Expr
	fieldProjection []rowFieldProjection
	raw             *parser.RowExpr
	sources         []int
	wholeNames      []string
	wholeName       string
	wholeSource     wholeRowSource
	presenceSlot    int
	presenceFrame   uint32
	mergeSource     bool
	returningWhole  bool   // Direct whole-row member projection uses the RETURNING output frame.
	returningNull   bool   // DELETE's scalar NEW whole row is NULL, independently of projection.
	ruleSide        uint32 // Resolved OLD/NEW whole-row identity, including zero members.
	expansions      map[int]compositeExpansionBinding
}

func (n rowExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := beginEvaluation(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.returningNull {
		return sqltype.Null(n.Usage.Type), nil
	}
	absent, err := runtime.RowAbsent(n)
	if err != nil {
		return sqltype.Value{}, err
	}
	if absent {
		return sqltype.Null(n.Usage.Type), nil
	}
	size := len(n.fields)
	if n.fieldProjection != nil {
		size = n.Usage.Type.BaseType().CompositeLen()
	}
	values := make([]sqltype.Value, size)
	for i, child := range n.fields {
		slot := i
		if n.fieldProjection != nil {
			slot = n.fieldProjection[i].slot
			if slot < 0 {
				continue
			}
		}
		v, err := child.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[slot] = v
	}
	return sqltype.OwnedCompositeValue(n.Usage.Type, values)
}

// Planning-only lookup; field evaluation retains its already resolved child.
func (n rowExpression) fieldSourceIndex(live int) int {
	if n.fieldProjection == nil {
		return live
	}
	for source, projection := range n.fieldProjection {
		if projection.slot == live {
			return source
		}
	}
	return -1
}

func (n rowExpression) retagFieldProjection(shape *sqltype.Descriptor) (rowExpression, bool, error) {
	old := n.Usage.Type.BaseType()
	if n.raw == nil || old.ID() == 2249 || !old.IsComposite() || !shape.IsComposite() {
		return n, false, nil
	}
	if n.fieldProjection == nil && shape.CompositeLen() >= old.CompositeLen() {
		return n, false, nil
	}
	projection := n.fieldProjection
	changed := false
	if projection == nil {
		if len(n.fields) != old.CompositeLen() {
			return n, false, fmt.Errorf("stuntgres: invalid retained row source shape")
		}
		projection = make([]rowFieldProjection, len(n.fields))
		changed = true
		for i := range projection {
			projection[i].attribute = old.CompositeMember(i).Attribute
		}
	}
	live := 0
	for i, original := range projection {
		slot, present := shape.CompositeAttributeIndex(original.attribute)
		if !present {
			slot = -1
		} else {
			live++
		}
		if !changed && original.slot != slot {
			projection = append([]rowFieldProjection(nil), projection...)
			changed = true
		}
		if changed {
			projection[i].slot = slot
		}
	}
	if live != shape.CompositeLen() {
		return n, false, fmt.Errorf("stuntgres: incompatible retained row shape")
	}
	n.fieldProjection = projection
	return n, changed, nil
}

type fieldExpression struct {
	expr.Info
	base      expr.Expr
	attribute int32
	index     int
}

func (n fieldExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	// Analysis/preflight still visits every constructor child. Runtime demand
	// may bypass its allocation and unused volatile children.
	if row, ok := n.base.(rowExpression); ok {
		runtime, err := nodeRuntimeOf(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		absent, err := runtime.RowAbsent(row)
		if err != nil {
			return sqltype.Value{}, err
		}
		if absent {
			return sqltype.Null(n.Usage.Type), nil
		}
		return row.fields[n.index].Eval(ctx)
	}
	value, err := n.base.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	index, ok := value.Type().BaseType().CompositeAttributeIndex(n.attribute)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: retired composite attribute")
	}
	return value.CompositeField(index), nil
}

type compositeExpansionBinding struct {
	base       expr.Expr
	fields     []expr.Expr
	names      []string
	attributes []int32
}
