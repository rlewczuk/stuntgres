package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// View qualification is a relational guard, not a change to ordinary Boolean
// expression evaluation. Unsafe user work never sees a row hidden by any layer.
type viewQualificationExpression struct {
	expr.Info
	predicates []expr.Expr // outer-to-inner storage; evaluated inner-to-outer
	user       expr.Expr
}

func (e viewQualificationExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	value, known, err := queryPredicateProofValue(c, e.user)
	if err != nil {
		return sqltype.Value{}, err
	}
	if known {
		truth, err := TruthOf(value)
		if err != nil {
			return sqltype.Value{}, err
		}
		if truth != True {
			// A proven empty user input cannot expose a row or demand a
			// retained qualification. Preserve its original FALSE/NULL value.
			return value, c.Context.Err()
		}
	}
	for i := len(e.predicates) - 1; i >= 0; i-- {
		value, err := e.predicates[i].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		truth, err := TruthOf(value)
		if err != nil {
			return sqltype.Value{}, err
		}
		if truth != True {
			return sqltype.Boolean(e.Usage.Type, false), nil
		}
	}
	if known {
		return value, c.Context.Err()
	}
	if e.user == nil {
		return sqltype.Boolean(e.Usage.Type, true), c.Context.Err()
	}
	return e.user.Eval(c)
}
func mapViewQualification(e viewQualificationExpression, mapper func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, bool, error) {
	changed := false
	var private []expr.Expr
	for i, x := range e.predicates {
		next, different, err := mapper(x)
		if err != nil {
			return nil, false, err
		}
		if different {
			if private == nil {
				private = append([]expr.Expr(nil), e.predicates...)
			}
			private[i] = next
			changed = true
		}
	}
	next, different, err := mapper(e.user)
	if err != nil {
		return nil, false, err
	}
	if different {
		e.user = next
		changed = true
	}
	if private != nil {
		e.predicates = private
	}
	return e, changed, nil
}
