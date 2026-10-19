package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (e commitTimestampExpression) Children() []expr.Expr {
	if e.arg == nil {
		return nil
	}
	return []expr.Expr{e.arg}
}

func (e commitTimestampExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := e.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(commitTimestampExpression)
	if err := rebuilt.validate(); err != nil {
		return nil, err
	}
	return rebuilt, nil
}

func (e commitTimestampExpression) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.Children()) {
		return nil, fmt.Errorf("stuntgres: invalid commitTimestampExpression arguments")
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

func (e commitTimestampExpression) SourceAdmission() (bool, bool) { return true, false }

func (e commitTimestampExpression) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	c := evalContext(ctx)
	value, err := e.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
