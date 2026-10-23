package stored

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The original enum carrier and the installed provider are different roles.
// Keep the original beside its hard manifest; only the validated current
// declaration may enter intagg's exact-current provider association guard.
func currentIntaggEnumerator(read catalog.Read, node plan.UnnestExpr) (*catalog.Procedure, error) {
	if node.Procedure == nil || node.RoutineReference != node.Procedure || len(node.Args) != 1 || node.Args[0] == nil || node.Args[0].ResultUsage().Type == nil || node.ResultUsage().Type == nil || node.ResultUsage().Type.ID() != sqltype.ID(node.Procedure.Result) {
		return nil, fmt.Errorf("stuntgres: invalid stored intagg enumerator")
	}
	if _, err := node.Rebuild(node.Info, node.Args); err != nil {
		return nil, err
	}
	current, _, err := currentInstalledProcedure(read, node.Ref, node.Procedure)
	if err != nil {
		return nil, err
	}
	if err := extension.ValidateIntaggIdentity(read, current, node.Ref, "int_array_enum"); err != nil {
		return nil, err
	}
	return current, nil
}

func (v *View) IntaggRecipeProcedure(catalog catalog.Read, path analyze.ViewLiteralPath, name string) (*catalog.Procedure, error) {
	ref, exists := v.routineRefs[path]
	consumer, _ := catalog.Relation(v.consumer)
	if !exists || ref.Address.ClassID != ids.ProcedureClassID || ref.Address.SubID != 0 || !ViewReferenceValid(catalog, consumer, ref) || ref.Epoch != catalog.Epoch() && (consumer == nil || ViewOf(consumer) != v) {
		return nil, fmt.Errorf("stuntgres: missing or stale frozen intagg reference")
	}
	proc, ok := catalog.Procedure(ref.Address)
	if !ok || extension.IntaggProviderOf(proc) != extension.StockIntagg {
		return nil, fmt.Errorf("stuntgres: mismatched frozen intagg procedure")
	}
	symbol, err := extension.ProcedureSymbol(catalog, proc)
	if err != nil || symbol != name {
		return nil, fmt.Errorf("stuntgres: mismatched frozen intagg procedure")
	}
	if err := extension.StockIntagg.ValidateProcedure(catalog, proc); err != nil {
		return nil, err
	}
	return proc, nil
}

// ProviderRecipeProcedure resolves only the recorded occurrence address. Its
// internal syntax carries the released provider symbol, never a public alias
// that could be re-resolved after rename or schema movement.
func (v *View) ProviderRecipeProcedure(read catalog.Read, path analyze.ViewLiteralPath, raw parser.Expr) (*catalog.Procedure, error) {
	ref, exists := v.routineRefs[path]
	consumer, _ := read.Relation(v.consumer)
	if !exists || ref.Address.ClassID != ids.ProcedureClassID || ref.Address.SubID != 0 || !ViewReferenceValid(read, consumer, ref) || ref.Epoch != read.Epoch() && (consumer == nil || ViewOf(consumer) != v) {
		return nil, fmt.Errorf("stuntgres: missing or stale frozen provider reference")
	}
	p, ok := read.Procedure(ref.Address)
	if !ok || p.Builtin == nil && (p.Implementation == nil || p.Extension == (ids.ObjectAddress{})) {
		return nil, fmt.Errorf("stuntgres: mismatched frozen provider procedure")
	}
	if err := (ProcedureProvider{}).Validate(read, p); err != nil {
		return nil, err
	}
	if _, err := exec.ValidateStoredRoutineDescriptor(read, p); err != nil {
		return nil, err
	}
	symbol := ""
	if p.Builtin != nil {
		symbol = p.Builtin.Identity.Name
	} else {
		var err error
		symbol, err = extension.ProcedureSymbol(read, p)
		if err != nil {
			return nil, err
		}
	}
	var shape parser.FunctionExpr
	switch call := raw.(type) {
	case parser.FunctionExpr:
		if !call.StoredProviderRoutine || call.StoredSQLRoutine || call.StoredProviderSymbol != symbol || len(call.Name.Parts) == 0 || p.RoutineKind != catalog.FunctionRoutine {
			return nil, fmt.Errorf("stuntgres: mismatched frozen provider syntax")
		}
		shape = call
	case parser.AggregateExpr:
		if !call.StoredProviderRoutine || call.StoredAggregate || call.StoredProviderSymbol != symbol || call.Window == nil && p.RoutineKind != catalog.AggregateRoutine || call.Window != nil && p.RoutineKind != catalog.WindowRoutine && p.RoutineKind != catalog.AggregateRoutine {
			return nil, fmt.Errorf("stuntgres: mismatched frozen provider syntax")
		}
		shape = parser.FunctionExpr{Args: call.Args, Variadic: call.Variadic}
		if call.Within {
			shape.Args = append([]parser.Expr(nil), call.Args...)
			for _, key := range call.Order {
				shape.Args = append(shape.Args, key.Expr)
			}
		}
	default:
		return nil, fmt.Errorf("stuntgres: mismatched frozen provider syntax")
	}
	known, valid := functions.StoredProviderCallShape(symbol, shape)
	if !known {
		_, valid = analyze.SQLCallableShape(read, nil, p, shape)
	}
	if !valid {
		return nil, fmt.Errorf("stuntgres: mismatched frozen provider call shape")
	}
	for _, dependency := range v.dependencies {
		if dependency.Ref == ref && dependency.Revision != 0 {
			return p, nil
		}
	}
	return nil, fmt.Errorf("stuntgres: missing frozen provider dependency")
}

// The caller has already validated the occurrence and its provider descriptor.
func (v *QueryRenderer) providerProcedureName(read catalog.Read, p *catalog.Procedure) (string, error) {
	if v.Presentation.Deparse && regobj.NamedObjectVisible(read, v.Presentation.Path, p) {
		return v.Presentation.identifier(p.Name()), nil
	}
	ns, ok := read.Namespace(p.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing frozen provider namespace")
	}
	return v.Presentation.identifier(ns.Name()) + "." + v.Presentation.identifier(p.Name()), nil
}
