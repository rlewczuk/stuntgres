package exec

import (
	"context"
	"database/sql/driver"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func foreignSlot(usage sqltype.Usage, id int) (catalog.ForeignQuerySlot, bool) {
	if usage.Type == nil || !usage.Type.IsBuiltin() || usage.Type.IsDomain() {
		return catalog.ForeignQuerySlot{}, false
	}
	switch usage.Type.ID() {
	case sqltype.Bool, sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Text:
	default:
		return catalog.ForeignQuerySlot{}, false
	}
	return catalog.ForeignQuerySlot{ID: id, TypeName: usage.Type.Name(), TypeOID: uint32(usage.Type.ID()), TypeModifier: usage.Typmod, CollationOID: usage.Collation.ID, Nullable: true}, true
}

func foreignComparable(e expr.Expr) bool {
	u := e.ResultUsage()
	return u.Type != nil && (u.Type.ID() != sqltype.Text || u.Collation.ID == 950 && u.Collation.Valid())
}

func foreignExpression(ctx context.Context, input ForeignPlanningInput, e expr.Expr, width int) (out catalog.ForeignQueryExpression, eligible bool, failure error) {
	var childError error
	defer func() {
		if childError != nil {
			eligible = false
			failure = childError
		}
	}()
	if e == nil {
		return out, false, nil
	}
	info := expr.InfoOf(e)
	if info.Property.Volatility != expr.Immutable && !(info.Property.Volatility == expr.Stable && info.Property.ParameterDependent) || info.Property.DomainDependent || info.Property.RuntimeDomainConversion || info.CollationConflict || info.RoutineRecipeReference != nil {
		return out, false, nil
	}
	typ, ok := foreignSlot(e.ResultUsage(), 0)
	if !ok {
		return out, false, nil
	}
	out.Type = typ
	out.Position = e.SourcePosition().Character
	child := func(value expr.Expr) bool {
		v, valid, err := foreignExpression(ctx, input, value, width)
		if err != nil || !valid {
			childError = err
			return false
		}
		out.Arguments = append(out.Arguments, v)
		return true
	}
	switch n := e.(type) {
	case plan.ColumnExpr:
		if n.Virtual != nil || n.Scope != 1 || n.Frame != input.Frame || n.Slot < 0 || n.Slot >= width || n.MergeSource {
			return out, false, nil
		}
		out.Kind, out.Slot = "slot", n.Slot
	case plan.GroupExpr:
		if n.Frame != input.Frame || n.Slot < 0 || n.Slot >= width {
			return out, false, nil
		}
		out.Kind, out.Slot = "slot", n.Slot
	case plan.CachedOrderExpr:
		if n.Slot < 0 {
			return out, false, nil
		}
		out.Kind, out.Slot = "slot", width+n.Slot
	case plan.LiteralExpr:
		cell, err := input.ExportValue(ctx, n.Value, input.Output)
		if err != nil {
			return out, false, err
		}
		out.Kind = "literal"
		out.Literal = CopyForeignCells([]driver.Value{cell})[0]
	case plan.ParameterExpr:
		out.Kind, out.Parameter = "parameter", n.Number
	case plan.ComparisonExpr:
		procedure, valid := info.RoutineReference.(*catalog.Procedure)
		if n.Namespace != "" && n.Namespace != "pg_catalog" || !valid || procedure.Builtin == nil || procedure.Builtin.Identity.Namespace != 11 || procedure.Builtin.Identity.ID != procedure.Address().ObjectID || procedure.Builtin.Volatility != expr.Immutable || procedure.Volatility != expr.Immutable || !foreignComparable(n.Left) || !foreignComparable(n.Right) {
			return out, false, nil
		}
		switch n.Operator {
		case "=", "<>", "<", ">", "<=", ">=":
		default:
			return out, false, nil
		}
		out.Kind, out.Operator, out.Namespace, out.RoutineOID = "comparison", n.Operator, "pg_catalog", procedure.RoutineOID()
		out.RoutineName = procedure.Builtin.Identity.Name
		if !child(n.Left) || !child(n.Right) {
			return out, false, nil
		}
	case plan.LogicalExpr:
		if n.QueryRight {
			return out, false, nil
		}
		switch n.Op {
		case parser.LogicalAnd:
			out.Kind = "and"
		case parser.LogicalOr:
			out.Kind = "or"
		case parser.LogicalNot:
			out.Kind = "not"
		default:
			return out, false, nil
		}
		if !child(n.Left) || n.Right != nil && !child(n.Right) {
			return out, false, nil
		}
	case plan.NullTestExpr:
		out.Kind = "isnull"
		if n.Not {
			out.Kind = "isnotnull"
		}
		if !child(n.Child) {
			return out, false, nil
		}
	case plan.CollationExpr:
		if n.Usage.Collation.ID != 950 || !n.Usage.Collation.Valid() {
			return out, false, nil
		}
		out.Kind = "collate"
		if !child(n.Child) {
			return out, false, nil
		}
	case plan.CoercionExpr:
		a, b := n.Child.ResultUsage().Type.ID(), n.Usage.Type.ID()
		if !(a == b || a == sqltype.Int2 && (b == sqltype.Int4 || b == sqltype.Int8) || a == sqltype.Int4 && b == sqltype.Int8) || !child(n.Child) {
			return out, false, nil
		}
		out.Kind = "cast"
	default:
		return out, false, nil
	}
	return out, true, nil
}

func firstForeignExporter(state *State) ForeignValueExporter {
	if state.Source.Foreign != nil {
		return state.Source.Foreign.ExportValue
	}
	for _, source := range state.Source.Sources {
		if source != nil && source.Foreign != nil {
			return source.Foreign.ExportValue
		}
	}
	return nil
}
