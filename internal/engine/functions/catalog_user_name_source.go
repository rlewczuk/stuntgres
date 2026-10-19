package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (e CatalogUserName) Children() []expr.Expr {
	return []expr.Expr{e.Arg}
}

func (e CatalogUserName) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := e.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(CatalogUserName)
	if err := rebuilt.Validate(); err != nil {
		return nil, err
	}
	return rebuilt, nil
}

func (e CatalogUserName) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.Children()) {
		return nil, fmt.Errorf("stuntgres: invalid CatalogUserName arguments")
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

func (e CatalogUserName) SourceAdmission() (bool, bool) { return true, false }

func (e CatalogUserName) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	c := ctx
	value, err := e.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
