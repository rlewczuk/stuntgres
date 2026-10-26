package plan

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Collate derives input authority from actual/default children through the
// caller's shared collation resolver, separately from the return type's Usage.
func (n SQLRoutineExpr) Collate(derive func(expr.Info, ...expr.Expr) (expr.Info, error)) (SQLRoutineExpr, error) {
	n.InputCollation = sqltype.CollationRef{}
	input := expr.Info{Pos: n.Pos}
	for _, arg := range n.Args {
		if arg.ResultUsage().Collation.ID != 0 {
			input.Usage = arg.ResultUsage()
			break
		}
	}
	if input.Usage.Type == nil {
		return n, nil
	}
	input, err := derive(input, n.Args...)
	if err != nil {
		return SQLRoutineExpr{}, err
	}
	if !input.CollationConflict {
		n.InputCollation = input.Usage.Collation
	}
	if n.Usage.Collation.ID != 0 {
		n.Usage.Collation = input.Usage.Collation
		n.CollationExplicit = input.CollationExplicit
		n.CollationConflict = input.CollationConflict
		n.CollationLeft, n.CollationRight, n.CollationConflictPos = input.CollationLeft, input.CollationRight, input.CollationConflictPos
		n.Signature.Result = n.Usage
	}
	return n, nil
}

func (n SQLRoutineExpr) Retag(info expr.Info, children []expr.Expr, changed bool, environment expr.Retag) (expr.Expr, bool, error) {
	rebuilt, err := n.Rebuild(info, children)
	if err != nil {
		return nil, false, err
	}
	next, err := rebuilt.(SQLRoutineExpr).Collate(environment.Collated)
	if err != nil {
		return nil, false, err
	}
	return next, changed || next.InputCollation != n.InputCollation || next.Usage != n.Usage || next.CollationExplicit != n.CollationExplicit || next.CollationConflict != n.CollationConflict || next.CollationLeft != n.CollationLeft || next.CollationRight != n.CollationRight || next.CollationConflictPos != n.CollationConflictPos, nil
}
