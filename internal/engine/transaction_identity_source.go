package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (e transactionIdentityExpression) Children() []expr.Expr {
	if e.arg == nil {
		return nil
	}
	return []expr.Expr{e.arg}
}

func (e transactionIdentityExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := e.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(transactionIdentityExpression)
	if err := rebuilt.validate(); err != nil {
		return nil, err
	}
	return rebuilt, nil
}

func (e transactionIdentityExpression) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.Children()) {
		return nil, fmt.Errorf("stuntgres: invalid transactionIdentityExpression arguments")
	}
	for _, child := range children {
		if child == nil {
			return nil, fmt.Errorf("stuntgres: nil source argument")
		}
	}
	e.Info = info
	if len(children) == 1 {
		e.arg = children[0]
	}
	return e, nil
}

func (e transactionIdentityExpression) SourceAdmission() (bool, bool) {
	return true, e.kind == identityMembers
}

func (e transactionIdentityExpression) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	c := evalContext(ctx)
	if e.kind == identityMembers {
		values, err := e.openMembers(c)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.OwnedSource(e.Usage.Type, values), nil
	}
	value, err := e.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
