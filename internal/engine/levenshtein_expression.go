package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

type installedLevenshteinFunctionExpression struct {
	functions.Levenshtein
	procedure *procedureDescriptor
	ref       ids.ObjectRef
}

// Rebuild keeps the installed-routine identity; the promoted Rebuild would
// return the bare functions node.
func (e installedLevenshteinFunctionExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	node, err := e.Levenshtein.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.Levenshtein = node.(functions.Levenshtein)
	return e, nil
}

func (n installedLevenshteinFunctionExpression) validate(v CatalogRead) error {
	for _, arg := range n.Args {
		if arg == nil || arg.ResultUsage().Type == nil {
			return fmt.Errorf("stuntgres: missing stored Levenshtein argument")
		}
	}
	proc, ok := v.Procedure(n.ref.Address)
	if !ok || proc != n.procedure || fuzzystrmatchProviderOf(proc) != stockFuzzystrmatch || proc.Name() != n.Function || proc.Signature != procedureSignature(functions.LevenshteinSignature(n.Function, len(n.Args))) || proc.Result != 23 {
		return fmt.Errorf("stuntgres: invalid stored Levenshtein identity")
	}
	if err := stockFuzzystrmatch.validateProcedure(v, proc); err != nil {
		return err
	}
	return n.Validate()
}
