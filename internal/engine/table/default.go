package table

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
)

func ValidateCookedDefaultTemporary(node expr.Expr, consumer ids.SessionToken) error {
	var err error
	plan.WalkExpression(node, func(node expr.Expr) {
		if err != nil {
			return
		}
		if call, ok := node.(functions.SequenceCall); ok {
			err = sequence.ValidateDefaultTemporaryProvider(call.Temporary, consumer, parser.Position{})
		}
	})
	return err
}
