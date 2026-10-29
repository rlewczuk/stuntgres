package stored

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func tablefuncCrosstabRecipe(raw parser.Expr) bool {
	call, ok := raw.(parser.FunctionExpr)
	if !ok || len(call.Name.Parts) == 0 {
		return false
	}
	switch call.Name.Parts[len(call.Name.Parts)-1].Text {
	case "crosstab", "crosstab2", "crosstab3", "crosstab4":
		return true
	}
	return false
}
func (v *View) tablefuncCrosstabRecipeProcedure(read catalog.Read, path analyze.ViewLiteralPath) (*catalog.Procedure, error) {
	ref, exists := v.routineRefs[path]
	consumer, _ := read.Relation(v.consumer)
	if !exists || ref.Address.ClassID != ids.ProcedureClassID || ref.Address.SubID != 0 || !ViewReferenceValid(read, consumer, ref) || ref.Epoch != read.Epoch() && (consumer == nil || ViewOf(consumer) != v) {
		return nil, fmt.Errorf("stuntgres: missing or stale frozen crosstab reference")
	}
	proc, ok := read.Procedure(ref.Address)
	if !ok || extension.TablefuncProviderOf(proc) != extension.StockTablefunc {
		return nil, fmt.Errorf("stuntgres: mismatched frozen crosstab procedure")
	}
	symbol, err := extension.ProcedureSymbol(read, proc)
	if err != nil {
		return nil, err
	}
	if !tablefuncCrosstabSymbol(symbol) {
		return nil, fmt.Errorf("stuntgres: mismatched frozen crosstab symbol")
	}
	return proc, nil
}
func (v *QueryRenderer) tablefuncCrosstabRecipeName(read catalog.Read, path analyze.ViewLiteralPath) (string, error) {
	proc, err := v.tablefuncCrosstabRecipeProcedure(read, path)
	if err != nil {
		return "", err
	}
	ns, ok := read.Namespace(proc.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing frozen crosstab namespace")
	}
	return v.Presentation.identifier(ns.Name()) + "." + v.Presentation.identifier(proc.Name()), nil
}
