package engine

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The embedded name selects the shared v4 kernel only. SQL authority and stored
// recipes always use the captured installed procedure, never a builtin alias.
type installedUUIDOsspFunctionExpression struct {
	functions.UUIDGeneration[BoundExpr, *EvalContext, ExprProperties]
	procedure *procedureDescriptor
	ref       ObjectRef
}

func (b *expressionBinder) bindInstalledUUIDOssp(raw parser.FunctionExpr) (BoundExpr, error) {
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
				name = b.diagnosticTypeName(o.Usage.Type)
			}
			names[i] = functionNamedType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	issue := functions.FunctionLabels(raw)
	if issue.Malformed {
		return nil, fmt.Errorf("invalid function argument labels")
	}
	if issue.Positional {
		return nil, expressionError("42601", "positional argument cannot follow named argument", issue.Position)
	}
	if issue.Duplicate != "" {
		return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", issue.Duplicate), issue.Position)
	}
	failure := func() (BoundExpr, error) { return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names) }
	auth, err := b.bindingAuthorization()
	if err != nil {
		return nil, err
	}
	path, err := b.engine.lookupNamespacesSnapshot(auth, raw.Name, installedFunctionLookup)
	if err != nil {
		return nil, err
	}
	if len(raw.Args) != 0 || raw.Variadic || len(raw.ArgNames) != 0 || raw.Star {
		return failure()
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	proc := procedureInNamespaces(auth.catalog, path, name, "")
	if proc == nil || proc.uuidOssp != stockUUIDOssp {
		return failure()
	}
	if err := stockUUIDOssp.validateProcedure(auth.catalog, proc); err != nil {
		return nil, err
	}
	switch {
	case raw.Distinct:
		return nil, expressionError("42809", "DISTINCT specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return nil, expressionError("42809", "ORDER BY specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return nil, expressionError("42809", "FILTER specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
	case raw.Window != nil:
		return nil, expressionError("42809", "OVER specified, but "+objectNameText(raw.Name)+" is not a window function nor an aggregate function", raw.Pos)
	}
	info := b.info(b.typ(sqltype.UUID).Usage(), raw.Pos, ExprProperties{Volatility: Volatile})
	info.routineReference = proc
	call, err := functions.NewUUIDGeneration[BoundExpr, *EvalContext](functionInfo(info), "uuidv4", nil, nil)
	if err != nil {
		return nil, err
	}
	ref := ObjectRef{owner: b.engine.owner, Epoch: auth.catalog.Epoch(), Address: proc.address}
	b.dependency(ref, proc.revision)
	return installedUUIDOsspFunctionExpression{UUIDGeneration: call, procedure: proc, ref: ref}, nil
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
