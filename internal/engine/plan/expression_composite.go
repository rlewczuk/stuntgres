package plan

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type WholeRowSource struct {
	Namespace, Qualifier int
	Frame                uint32
}

// A retained named ROW keeps written children for metadata and dependencies.
// Only a real shape change installs this immutable stable-attribute projection.
type RowFieldProjection struct {
	Attribute int32
	Slot      int // Current live member, or -1 when that original attribute retired.
}

type RowExpr struct {
	expr.Info
	Fields          []expr.Expr
	FieldProjection []RowFieldProjection
	Raw             *parser.RowExpr
	Sources         []int
	WholeNames      []string
	WholeName       string
	WholeSource     WholeRowSource
	PresenceSlot    int
	PresenceFrame   uint32
	MergeSource     bool
	ReturningWhole  bool   // Direct whole-row member projection uses the RETURNING output frame.
	ReturningNull   bool   // DELETE's scalar NEW whole row is NULL, independently of projection.
	RuleSide        uint32 // Resolved OLD/NEW whole-row identity, including zero members.
	Expansions      map[int]CompositeExpansionBinding
}

func (n RowExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := BeginEvaluation(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.ReturningNull {
		return sqltype.Null(n.Usage.Type), nil
	}
	absent, err := runtime.RowAbsent(n)
	if err != nil {
		return sqltype.Value{}, err
	}
	if absent {
		return sqltype.Null(n.Usage.Type), nil
	}
	size := len(n.Fields)
	if n.FieldProjection != nil {
		size = n.Usage.Type.BaseType().CompositeLen()
	}
	values := make([]sqltype.Value, size)
	for i, child := range n.Fields {
		slot := i
		if n.FieldProjection != nil {
			slot = n.FieldProjection[i].Slot
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
func (n RowExpr) FieldSourceIndex(live int) int {
	if n.FieldProjection == nil {
		return live
	}
	for source, projection := range n.FieldProjection {
		if projection.Slot == live {
			return source
		}
	}
	return -1
}

func (n RowExpr) RetagFieldProjection(shape *sqltype.Descriptor) (RowExpr, bool, error) {
	old := n.Usage.Type.BaseType()
	if n.Raw == nil || old.ID() == 2249 || !old.IsComposite() || !shape.IsComposite() {
		return n, false, nil
	}
	if n.FieldProjection == nil && shape.CompositeLen() >= old.CompositeLen() {
		return n, false, nil
	}
	projection := n.FieldProjection
	changed := false
	if projection == nil {
		if len(n.Fields) != old.CompositeLen() {
			return n, false, fmt.Errorf("stuntgres: invalid retained row source shape")
		}
		projection = make([]RowFieldProjection, len(n.Fields))
		changed = true
		for i := range projection {
			projection[i].Attribute = old.CompositeMember(i).Attribute
		}
	}
	live := 0
	for i, original := range projection {
		slot, present := shape.CompositeAttributeIndex(original.Attribute)
		if !present {
			slot = -1
		} else {
			live++
		}
		if !changed && original.Slot != slot {
			projection = append([]RowFieldProjection(nil), projection...)
			changed = true
		}
		if changed {
			projection[i].Slot = slot
		}
	}
	if live != shape.CompositeLen() {
		return n, false, fmt.Errorf("stuntgres: incompatible retained row shape")
	}
	n.FieldProjection = projection
	return n, changed, nil
}

type FieldExpr struct {
	expr.Info
	Base      expr.Expr
	Attribute int32
	Index     int
}

func (n FieldExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	// Analysis/preflight still visits every constructor child. Runtime demand
	// may bypass its allocation and unused volatile children.
	if row, ok := n.Base.(RowExpr); ok {
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
		return row.Fields[n.Index].Eval(ctx)
	}
	value, err := n.Base.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	index, ok := value.Type().BaseType().CompositeAttributeIndex(n.Attribute)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: retired composite attribute")
	}
	return value.CompositeField(index), nil
}

type CompositeExpansionBinding struct {
	Base       expr.Expr
	Fields     []expr.Expr
	Names      []string
	Attributes []int32
}
