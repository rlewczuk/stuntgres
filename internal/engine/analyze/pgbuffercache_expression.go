package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (r installedExtensionRoutine) attachPgBuffercache(call functions.PgBuffercache, ref ids.ObjectRef) (expr.Expr, error) {
	b := r.binder
	if err := extension.ValidatePgBuffercacheIdentity(r.catalog, r.procedure, ref); err != nil {
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
	call.RoutineReference = r.procedure
	b.dependency(ref, r.procedure.Revision())
	return plan.InstalledPgBuffercacheExpr{PgBuffercache: call, Procedure: r.procedure, Ref: ref}, nil
}
