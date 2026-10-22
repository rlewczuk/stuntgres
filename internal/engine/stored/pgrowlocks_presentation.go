package stored

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func pgrowlocksRecipe(raw parser.Expr) bool {
	call, ok := raw.(parser.FunctionExpr)
	return ok && len(call.Name.Parts) != 0 && call.Name.Parts[len(call.Name.Parts)-1].Text == "pgrowlocks"
}
func (v *View) pgrowlocksRecipeProcedure(read catalog.Read, path analyze.ViewLiteralPath) (*catalog.Procedure, error) {
	ref, exists := v.routineRefs[path]
	consumer, _ := read.Relation(v.consumer)
	if !exists || ref.Address.ClassID != ids.ProcedureClassID || ref.Address.SubID != 0 || !ViewReferenceValid(read, consumer, ref) || ref.Epoch != read.Epoch() && (consumer == nil || ViewOf(consumer) != v) {
		return nil, fmt.Errorf("stuntgres: missing or stale frozen pgrowlocks reference")
	}
	proc, ok := read.Procedure(ref.Address)
	if !ok || extension.PgrowlocksProviderOf(proc) != extension.StockPgrowlocks {
		return nil, fmt.Errorf("stuntgres: mismatched frozen pgrowlocks procedure")
	}
	if err := extension.StockPgrowlocks.ValidateProcedure(read, proc); err != nil {
		return nil, err
	}
	return proc, nil
}
func (v *QueryRenderer) pgrowlocksRecipeName(read catalog.Read, path analyze.ViewLiteralPath) (string, error) {
	proc, err := v.pgrowlocksRecipeProcedure(read, path)
	if err != nil {
		return "", err
	}
	namespace, ok := read.Namespace(proc.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing frozen pgrowlocks namespace")
	}
	return v.Presentation.identifier(namespace.Name()) + "." + v.Presentation.identifier(proc.Name()), nil
}
