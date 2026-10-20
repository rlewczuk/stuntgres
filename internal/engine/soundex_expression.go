package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

type installedSoundexFunctionExpression struct {
	functions.Soundex
	procedure *catalog.Procedure
	ref       ids.ObjectRef
}

// Rebuild keeps the installed-routine identity; the promoted Rebuild would
// return the bare functions node.
func (e installedSoundexFunctionExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	node, err := e.Soundex.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.Soundex = node.(functions.Soundex)
	return e, nil
}

func (n installedSoundexFunctionExpression) validate(v catalog.Read) error {
	for _, arg := range n.Args {
		if arg == nil || arg.ResultUsage().Type == nil {
			return fmt.Errorf("stuntgres: missing stored Soundex argument")
		}
	}
	result := ids.OID(25)
	if n.Function == "difference" {
		result = 23
	}
	proc, ok := v.Procedure(n.ref.Address)
	if !ok || proc != n.procedure || FuzzystrmatchProviderOf(proc) != StockFuzzystrmatch || proc.Name() != n.Function || proc.Signature != catalog.ProcedureSignature(functions.SoundexSignature(n.Function, len(n.Args))) || proc.Result != result {
		return fmt.Errorf("stuntgres: invalid stored Soundex identity")
	}
	if err := StockFuzzystrmatch.ValidateProcedure(v, proc); err != nil {
		return err
	}
	return n.Validate()
}
