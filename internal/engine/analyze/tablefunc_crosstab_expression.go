package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (r installedExtensionRoutine) attachTablefuncCrosstab(call functions.TablefuncCrosstab, ref ids.ObjectRef) (expr.Expr, error) {
	b := r.binder
	if err := extension.ValidateTablefuncIdentity(r.catalog, r.procedure, ref); err != nil {
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
	if r.procedure.Result != 2249 {
		call.Usage = b.Type(sqltype.ID(r.procedure.Result)).Usage()
		if err := b.CaptureTypeInput(call.Usage.Type); err != nil {
			return nil, err
		}
	} else {
		// Target-list SRF frames need typed NULL slots before OpenSource checks
		// the result context. The catalog RECORD identity is lookup-only;
		// use the shared anonymous-record carrier until FROM supplies a shape.
		call.Usage = sqltype.DeferredRecord().Usage()
	}
	b.dependency(ref, r.procedure.Revision())
	call.RoutineReference = r.procedure
	return plan.InstalledTablefuncCrosstabExpr{TablefuncCrosstab: call, Procedure: r.procedure, Ref: ref}, nil
}
