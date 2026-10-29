package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InstalledAutoincExpr retains released routine identity only, never an active
// trigger frame. Ordinary scalar invocation always uses the outside-trigger path.
type InstalledAutoincExpr struct {
	functions.TriggerBuiltin
	Procedure *catalog.Procedure
	Ref ids.ObjectRef
}

func (n InstalledAutoincExpr) ValidateStoredProvider() error {
	if err := n.TriggerBuiltin.Validate(); err != nil { return err }
	if n.Name != "autoinc" || n.Procedure == nil || n.RoutineReference != n.Procedure || n.Ref.Address != n.Procedure.Address() || n.Ref.Domain() == nil || n.Ref.Epoch == 0 {
		return fmt.Errorf("stuntgres: invalid installed autoinc carrier")
	}
	return nil
}
func (n InstalledAutoincExpr) Eval(ctx expr.Context) (sqltype.Value,error) {
	runtime,err:=nodeRuntimeOf(ctx)
	if err!=nil { return sqltype.Value{},err }
	return runtime.EvalInstalledAutoinc(n)
}
func (n InstalledAutoincExpr) OpenSource(ctx expr.Context) (expr.SourceCursor,error) {
	v,err:=n.Eval(ctx)
	if err!=nil { return expr.SourceCursor{},err }
	return expr.ScalarSource(v),nil
}
func (n InstalledAutoincExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr,error) {
	value,err:=n.TriggerBuiltin.Rebuild(info,children)
	if err!=nil { return nil,err }
	n.TriggerBuiltin=value.(functions.TriggerBuiltin)
	return n,n.ValidateStoredProvider()
}
func (n InstalledAutoincExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr,error) {
	return n.Rebuild(info,children)
}
func (n InstalledAutoincExpr) SameGroup(other expr.Expr,same func(expr.Expr,expr.Expr)bool,definition bool)bool {
	o,ok:=other.(InstalledAutoincExpr)
	return ok && n.Procedure==o.Procedure && n.Ref==o.Ref && n.TriggerBuiltin.SameGroup(o.TriggerBuiltin,same,definition)
}
