package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (e ObjectAddress) Children() []expr.Expr {
	return e.Args[:]
}

func (e ObjectAddress) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := e.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(ObjectAddress)
	rebuilt.Property = expr.Properties{Volatility: expr.Stable}
	for _, arg := range rebuilt.Args {
		rebuilt.Property = expr.MergeProperties(rebuilt.Property, arg.Properties())
	}
	if err := rebuilt.Validate(); err != nil {
		return nil, err
	}
	return rebuilt, nil
}

func (e ObjectAddress) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.Children()) {
		return nil, fmt.Errorf("stuntgres: invalid objectAddressExpression arguments")
	}
	for _, child := range children {
		if child == nil {
			return nil, fmt.Errorf("stuntgres: nil source argument")
		}
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}

func (e ObjectAddress) SourceAdmission() (bool, bool) { return true, false }

func (e ObjectAddress) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	c := ctx
	value, err := e.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
