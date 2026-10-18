package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The embedded name selects the shared v4 kernel only. SQL authority and stored
// recipes always use the captured installed procedure, never a builtin alias.
type installedUUIDOsspFunctionExpression struct {
	functions.UUIDGeneration
	procedure *procedureDescriptor
	ref       ids.ObjectRef
}

func bindInstalledUUIDOssp(b functions.Binder, raw parser.FunctionExpr) (BoundExpr, error) {
	var names []string
	if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if names != nil {
			name := "unknown"
			if o.Usage.Type != nil {
				name = b.DiagnosticTypeName(o.Usage.Type)
			}
			names[i] = functions.NamedArgumentType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := functions.CallLabelError(raw); err != nil {
		return nil, err
	}
	routine, err := b.InstalledRoutine(raw.Name, functions.UUIDOsspExtension, "")
	if err != nil {
		return nil, err
	}
	if len(raw.Args) != 0 || raw.Variadic || len(raw.ArgNames) != 0 || raw.Star || routine == nil {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	call, err := functions.NewUUIDGeneration(b.Info(b.Type(sqltype.UUID).Usage(), raw.Pos, expr.Properties{Volatility: expr.Volatile}), "uuidv4", nil, nil)
	if err != nil {
		return nil, err
	}
	return routine.Attach(call)
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
