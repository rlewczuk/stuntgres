package table

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// ColumnTypeEventRewrite follows ATColumnChangeRequiresRewrite on the actual
// checked conversion tree. Identity/relabel and simplified widening typmods are
// peeled; another attribute, virtual expression or real function rewrites.
func ColumnTypeEventRewrite(node expr.Expr, original *catalog.Relation, slot int, zone *sqltype.Zone) bool {
	for {
		switch n := node.(type) {
		case plan.ColumnExpr:
			if n.Virtual != nil {
				node = n.Virtual
				continue
			}
			origin := n.Origin.Address
			origin.SubID = 0
			return origin != original.Address() || n.Slot != slot
		case plan.CollationExpr:
			node = n.Child
		case plan.CoercionExpr:
			if n.Plan.HeapRewriteRequired(zone) {
				return true
			}
			node = n.Child
		case plan.DatumCastExpr:
			if n.Method != "b" || n.Size && n.Sizing.HeapRewriteRequired(zone) {
				return true
			}
			node = n.Child
		case functions.CastRoutine:
			function, ok := n.RoutineReference.(*catalog.Procedure)
			if !ok || function.Builtin == nil || n.Count != 1 || function.Address().ObjectID != 2027 && function.Address().ObjectID != 2028 || n.Plan.HeapRewriteRequired(zone) {
				return true
			}
			node = n.Args[0]
		default:
			return true
		}
	}
}

// AlterEventRewriteReason preserves all semantic causes independently of native
// row copying. Persistence and method changes are added by the relation owner.
func AlterEventRewriteReason(transformations []index.ColumnTransformation, stored []int32, perRowAdd bool) int32 {
	var reason int32
	if len(stored) != 0 || perRowAdd {
		reason |= 2
	}
	for _, transformation := range transformations {
		if transformation.EventRewrite {
			reason |= 4
		}
	}
	return reason
}
