package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e extensionPathsExpression) Children() []expr.Expr {
	return []expr.Expr{e.Arg}
}

func (e extensionPathsExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := e.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(extensionPathsExpression)
	if err := rebuilt.Validate(); err != nil {
		return nil, err
	}
	return rebuilt, nil
}

func (e extensionPathsExpression) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.Children()) {
		return nil, fmt.Errorf("stuntgres: invalid extensionPathsExpression arguments")
	}
	for _, child := range children {
		if child == nil {
			return nil, fmt.Errorf("stuntgres: nil source argument")
		}
	}
	e.Info = info
	e.Arg = children[0]
	return e, nil
}

func (e extensionPathsExpression) SourceAdmission() (bool, bool) { return true, true }

func (e extensionPathsExpression) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(extensionPathsExpression)
	return ok && sqltype.SameRecordUsage(e.ResultUsage(), o.ResultUsage())
}

func (e extensionPathsExpression) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	c := evalContext(ctx)
	values, err := e.open(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(e.Usage.Type, values), nil
}
