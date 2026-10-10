package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/parser"
)

// Both retained bound definitions and raw view trees keep the original four
// arguments; presentation chooses the ordinary name, not the SQL provenance.
func renderOverlapsCall[T any](syntax parser.FunctionSyntax, args []T, ordinary string, child func(int, T) (string, error)) (string, error) {
	if len(args) != 4 {
		return "", fmt.Errorf("stuntgres: invalid stored OVERLAPS arity")
	}
	var parts [4]string
	for i, arg := range args {
		text, err := child(i, arg)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	if syntax == parser.SQLFunctionSyntax {
		return "((" + parts[0] + ", " + parts[1] + ") OVERLAPS (" + parts[2] + ", " + parts[3] + "))", nil
	}
	return ordinary + "(" + parts[0] + ", " + parts[1] + ", " + parts[2] + ", " + parts[3] + ")", nil
}

func (e datetimeOverlapsExpression) logicalSQL(child func(BoundExpr) (string, error)) (string, error) {
	return renderOverlapsCall(e.syntax, e.args[:], "pg_catalog.overlaps", func(_ int, arg BoundExpr) (string, error) { return child(arg) })
}

func (e datetimeOverlapsExpression) publicSQL(ctx context.Context, r pgRenderer, child func(BoundExpr) (string, error)) (string, error) {
	local := e.plan.LocalClock()
	return renderOverlapsCall(e.syntax, e.args[:], quoteIdentifier("overlaps"), func(i int, arg BoundExpr) (string, error) {
		if e.syntax == parser.SQLFunctionSyntax {
			return child(arg)
		}
		argRenderer := r
		argRenderer.showImplicit = true
		text, err := argRenderer.sql(ctx, arg, pgRenderParent{functionArgument: true})
		if err != nil {
			return "", err
		}
		if local[i] {
			_, column := arg.(columnExpression)
			_, literal := arg.(literalExpression)
			return r.cast(text, e.plan.SignatureType(i).Usage(), column || literal)
		}
		return text, nil
	})
}
