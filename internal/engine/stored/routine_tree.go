package stored

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/parser"
)

// StructuralOutput retains the ordered actual command syntax of checked actions.
// No catalog, mutable execution state, or authorization is retained for output.
func (p *RoutineProgram) StructuralOutput(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString("{RoutineProgram")
	for _, action := range p.actions {
		if action.syntax == nil {
			return "", fmt.Errorf("stuntgres: missing routine command syntax")
		}
		out.WriteByte(' ')
		if err := parser.AppendStructuralSyntax(ctx, &out, action.syntax); err != nil {
			return "", err
		}
	}
	out.WriteByte('}')
	return out.String(), nil
}
