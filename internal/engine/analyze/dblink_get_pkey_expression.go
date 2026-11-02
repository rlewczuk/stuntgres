package analyze

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (r installedExtensionRoutine) attachDblinkGetPkey(call functions.DblinkGetPkey, ref ids.ObjectRef) (expr.Expr, error) {
	b := r.binder
	if err := extension.ValidateDblinkIdentity(r.catalog, r.procedure, ref); err != nil {
		return nil, err
	}
	symbol, err := extension.ProcedureSymbol(r.catalog, r.procedure)
	if err != nil {
		return nil, err
	}
	if symbol != functions.DblinkGetPkeyName {
		return nil, fmt.Errorf("stuntgres: mismatched dblink_get_pkey declaration")
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
	typ, ok := b.definitionType(sqltype.ID(r.procedure.Result))
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing dblink_get_pkey result")
	}
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: r.procedure.Result}
	descriptor, ok := b.definitionDescriptor(address)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing dblink_get_pkey result declaration")
	}
	// Capture the actual result generation without minting a new occurrence.
	b.dependency(b.Host.CatalogOwner().Domain.Ref(r.catalog.Epoch(), address), descriptor.Revision())
	call.Usage, call.RoutineReference = typ.Usage(), r.procedure
	if err := extension.ValidateDblinkGetPkeyCall(r.catalog, r.procedure, call); err != nil {
		return nil, err
	}
	b.dependency(ref, r.procedure.Revision())
	return plan.InstalledDblinkGetPkeyExpr{DblinkGetPkey: call, Procedure: r.procedure, Ref: ref}, nil
}
