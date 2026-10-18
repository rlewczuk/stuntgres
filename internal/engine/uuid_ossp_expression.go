package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// The embedded name selects the shared v4 kernel only. SQL authority and stored
// recipes always use the captured installed procedure, never a builtin alias.
type installedUUIDOsspFunctionExpression struct {
	functions.UUIDGeneration
	procedure *procedureDescriptor
	ref       ids.ObjectRef
}

func (n installedUUIDOsspFunctionExpression) validShape() bool {
	properties := n.Properties()
	// Group rewriting adds execution-stage dependence to a valid leaf.
	// DEFAULT closure separately rejects that dependence before shape checks.
	properties.StageDependent = false
	return n.procedure != nil && n.RoutineReference == n.procedure && n.procedure.uuidOssp == stockUUIDOssp && n.ref.Address == n.procedure.address && n.ref.Epoch != 0 && n.Name == "uuidv4" && len(n.Args) == 0 && n.Timestamp == nil && n.Addition == (sqltype.Arithmetic{}) && n.ResultUsage().Type != nil && n.ResultUsage().Type.ID() == sqltype.UUID && n.ResultUsage().Valid() && properties == (ExprProperties{Volatility: Volatile})
}

func (n installedUUIDOsspFunctionExpression) validate(v CatalogRead) error {
	if !n.validShape() || v.state.descriptors[n.ref.Address] != n.procedure {
		return fmt.Errorf("stuntgres: invalid stored uuid-ossp identity")
	}
	return stockUUIDOssp.validateProcedure(v, n.procedure)
}

// installedUUIDOsspRoutine is the procedure identity handed to bind code
// through functions.Binder.InstalledRoutine.
type installedUUIDOsspRoutine struct {
	binder    *expressionBinder
	catalog   CatalogRead
	procedure *procedureDescriptor
}

func (r installedUUIDOsspRoutine) Validate() error {
	return stockUUIDOssp.validateProcedure(r.catalog, r.procedure)
}

func (r installedUUIDOsspRoutine) Attach(call expr.Expr) (expr.Expr, error) {
	generation, ok := call.(functions.UUIDGeneration)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid uuid-ossp call")
	}
	generation.RoutineReference = r.procedure
	ref := r.binder.engine.catalog.domain.Ref(r.catalog.Epoch(), r.procedure.address)
	r.binder.dependency(ref, r.procedure.revision)
	return installedUUIDOsspFunctionExpression{UUIDGeneration: generation, procedure: r.procedure, ref: ref}, nil
}
