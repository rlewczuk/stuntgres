package exec

import (
	"fmt"

	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) EvalInstalledAutoinc(n plan.InstalledAutoincExpr) (sqltype.Value,error) {
	if err:=EvaluationContext(c);err!=nil { return sqltype.Value{},err }
	if err:=n.ValidateStoredProvider();err!=nil { return sqltype.Value{},err }
	auth,_,err:=RoutineAdmissionAuthority(c)
	if err!=nil { return sqltype.Value{},err }
	if !auth.Live() || n.Ref.Domain()!=auth.Catalog.Owner().Domain {
		return sqltype.Value{},fmt.Errorf("stuntgres: autoinc lacks current routine authority")
	}
	ref:=n.Ref
	if ref.Epoch!=auth.Catalog.Epoch() {
		if c.RoutineConsumer==nil || !preparation.StoredReferenceValid(auth.Catalog,c.RoutineConsumer,ref) {
			return sqltype.Value{},ids.ErrCatalogReplaced
		}
		ref.Epoch=auth.Catalog.Epoch()
	}
	current,err:=ValidateRoutineDescriptor(auth.Catalog,auth.Roles,n.Procedure)
	if err!=nil { return sqltype.Value{},err }
	if extension.AutoincProviderOf(current)!=extension.StockAutoinc {
		return sqltype.Value{},fmt.Errorf("stuntgres: invalid autoinc provider")
	}
	if err:=extension.ValidateAutoincIdentity(auth.Catalog,current,ref);err!=nil { return sqltype.Value{},err }
	return n.TriggerBuiltin.Eval(c)
}
