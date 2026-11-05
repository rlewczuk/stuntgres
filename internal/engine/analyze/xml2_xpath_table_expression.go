package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (r installedExtensionRoutine) attachXML2XPathTable(call functions.XML2XPathTable, ref ids.ObjectRef) (expr.Expr, error) {
	b := r.binder
	if err := extension.ValidateXML2XPathTableIdentity(r.catalog, r.procedure, ref); err != nil {
		return nil, err
	}
	if b.Params.ExecuteParameters {
		return nil, expr.ErrorAt("0A000", "set-returning functions are not allowed in EXECUTE parameters", call.Pos)
	}
	if b.Consumer.PolicyConsumer && b.Query.Frame == 0 {
		return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions")
	}
	if err := b.AdmitSetReturning(call.Pos); err != nil {
		return nil, err
	}
	call.Usage = sqltype.DeferredRecord().Usage()
	b.dependency(ref, r.procedure.Revision())
	call.RoutineReference = r.procedure
	return plan.InstalledXML2XPathTableExpr{XML2XPathTable: call, Procedure: r.procedure, Ref: ref}, nil
}
