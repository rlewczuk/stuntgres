package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

type installedMetaphoneFunctionExpression struct {
	functions.Metaphone
	procedure *catalog.Procedure
	ref       ids.ObjectRef
}

// Rebuild keeps the installed-routine identity; the promoted Rebuild would
// return the bare functions node.
func (e installedMetaphoneFunctionExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	node, err := e.Metaphone.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.Metaphone = node.(functions.Metaphone)
	return e, nil
}

func (n installedMetaphoneFunctionExpression) validate(v CatalogRead) error {
	for _, arg := range n.Args {
		if arg == nil || arg.ResultUsage().Type == nil {
			return fmt.Errorf("stuntgres: missing stored Metaphone argument")
		}
	}
	proc, ok := v.Procedure(n.ref.Address)
	if !ok || proc != n.procedure || fuzzystrmatchProviderOf(proc) != stockFuzzystrmatch || proc.Name() != n.Function || proc.Signature != catalog.ProcedureSignature(functions.MetaphoneSignature(n.Function, len(n.Args))) || proc.Result != 25 {
		return fmt.Errorf("stuntgres: invalid stored Metaphone identity")
	}
	if err := stockFuzzystrmatch.validateProcedure(v, proc); err != nil {
		return err
	}
	return n.Validate()
}
