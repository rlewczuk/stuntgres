package plan

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

// InstalledCubeExpr retains procedure authority independently of type identity.
type InstalledCubeExpr struct {
	functions.Cube
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (e InstalledCubeExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	node, err := e.Cube.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.Cube = node.(functions.Cube)
	return e, nil
}

func (e InstalledCubeExpr) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	cube := env.Type(e.CubeType)
	changed = changed || cube != e.CubeType
	e.CubeType = cube
	next, err := e.Rebuild(info, children)
	return next, changed, err
}

func (e InstalledCubeExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}

func (e InstalledCubeExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledCubeExpr)
	if !ok || e.Procedure != o.Procedure || e.Ref != o.Ref || e.Operation != o.Operation || e.CubeType != o.CubeType || len(e.Args) != len(o.Args) {
		return false
	}
	for i, arg := range e.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
