package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (e objectAddressExpression) Children() []expr.Expr {
	return e.args[:]
}

func (e objectAddressExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := e.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(objectAddressExpression)
	if err := rebuilt.validateStored(); err != nil {
		return nil, err
	}
	return rebuilt, nil
}

func (e objectAddressExpression) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.Children()) {
		return nil, fmt.Errorf("stuntgres: invalid objectAddressExpression arguments")
	}
	for _, child := range children {
		if child == nil {
			return nil, fmt.Errorf("stuntgres: nil source argument")
		}
	}
	e.Info = info
	copy(e.args[:], children)
	return e, nil
}

func (e objectAddressExpression) SourceAdmission() (bool, bool) { return true, false }

func (e objectAddressExpression) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	c := evalContext(ctx)
	value, err := e.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
