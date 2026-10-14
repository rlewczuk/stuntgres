package engine

import (
	"context"
	"fmt"
	"math"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
)

type capturedRegroleInput struct{ roles authorization.Read }

func (r capturedRegroleInput) ResolveRegrole(ctx context.Context, input string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	parts, err := parser.ParseRegclassName(input)
	if err != nil || len(parts.Parts) != 1 {
		return 0, sqlError("42602", "invalid name syntax")
	}
	name := parts.Parts[0].Text
	role, ok := r.roles.Lookup(name)
	if !ok {
		return 0, sqlError("42704", `role "`+name+`" does not exist`)
	}
	if role.Ref.ID() > math.MaxUint32 {
		return 0, fmt.Errorf("stuntgres: regrole identity out of range")
	}
	return uint32(role.Ref.ID()), nil
}
