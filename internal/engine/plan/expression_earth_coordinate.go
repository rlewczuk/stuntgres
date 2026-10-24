package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

type InstalledEarthExpr struct {
	functions.EarthCoordinate
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (e InstalledEarthExpr) ValidateStoredProvider() error {
	if err := e.Validate(); err != nil {
		return err
	}
	if e.Procedure == nil || e.RoutineReference != e.Procedure || e.Ref.Address != e.Procedure.Address() || e.Ref.Address.ClassID != ids.ProcedureClassID || e.Ref.Address.SubID != 0 || e.Procedure.Revision() == 0 || string(e.Procedure.Signature) != e.Signature() || e.Procedure.Result != ids.OID(e.Usage.Type.ID()) {
		return fmt.Errorf("invalid captured earth coordinate identity")
	}
	return nil
}
func (e InstalledEarthExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	n, err := e.EarthCoordinate.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.EarthCoordinate = n.(functions.EarthCoordinate)
	return e, nil
}
func (e InstalledEarthExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e InstalledEarthExpr) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	n, did, err := e.EarthCoordinate.Retag(info, children, changed, env)
	if err != nil {
		return nil, false, err
	}
	e.EarthCoordinate = n.(functions.EarthCoordinate)
	return e, did, nil
}
func (e InstalledEarthExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(InstalledEarthExpr)
	if !ok || e.Procedure != o.Procedure || e.Ref != o.Ref || e.Plan != o.Plan || len(e.Args) != len(o.Args) {
		return false
	}
	for i, arg := range e.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
