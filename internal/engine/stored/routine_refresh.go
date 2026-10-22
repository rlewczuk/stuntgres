package stored

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// RefreshRoutineCalls refreshes descriptor handles by captured identity only.
// A rename never reselects the callee or an overload from original SQL names.
func RefreshRoutineCalls(read catalog.Read, b analyze.Binding) (analyze.Binding, error) {
	mapper := NewRuleTreeMapper(read, nil, 0, nil, RuleTreeMapping{Leaf: func(node expr.Expr) (expr.Expr, bool, error) {
		call, ok := node.(plan.SQLRoutineExpr)
		if !ok {
			return node, false, nil
		}
		current, err := currentRoutineCall(read, call)
		if err != nil {
			return nil, false, err
		}
		if current == call.Procedure {
			return node, false, nil
		}
		call.Procedure, call.Info.RoutineReference = current, current
		return call, true, nil
	}})
	return MapRuleBindingRecipes(b, mapper)
}

func currentRoutineCall(read catalog.Read, call plan.SQLRoutineExpr) (*catalog.Procedure, error) {
	if call.Procedure == nil || call.Ref.Address != call.Procedure.Address() || read.Owner() == nil || call.Ref.Domain() != read.Owner().Domain || call.Ref.Epoch != read.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	current, ok := read.Procedure(call.Ref.Address)
	if !ok || current.SQL == nil || current.SQL != call.Procedure.SQL || current.Signature != call.Procedure.Signature || current.Result != call.Procedure.Result || current.RoutineKind != catalog.FunctionRoutine {
		return nil, ids.ErrCatalogReplaced
	}
	return current, nil
}
