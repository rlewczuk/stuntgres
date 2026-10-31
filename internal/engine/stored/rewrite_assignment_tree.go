package stored

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Assignment paths are immutable analyzed destinations, separate from RHS roots.
// Keep sparse target ordinals and chain/dimension order; no execution state or
// presentation spelling participates in this projection.
func appendRewriteAssignmentTargets(ctx context.Context, out *strings.Builder, owner string, relation *catalog.Relation, slots []int, targets []*plan.ArrayAssignmentTarget) error {
	for ordinal, target := range targets {
		if err := ctx.Err(); err != nil { return err }
		if target == nil { continue }
		if relation == nil || ordinal >= len(slots) || slots[ordinal] < 0 || slots[ordinal] >= len(relation.Attributes) || relation.Attributes[slots[ordinal]] <= 0 {
			return fmt.Errorf("stuntgres: invalid rewrite assignment target association")
		}
		fmt.Fprintf(out, " {assignment_target owner %q ordinal %d attribute %d", owner, ordinal, relation.Attributes[slots[ordinal]])
		for step := 0; target != nil; step, target = step+1, target.Next {
			if err := ctx.Err(); err != nil { return err }
			fmt.Fprintf(out, " {step %d member %d", step, target.Attribute)
			if err := appendRewriteTargetUsage(out, "container", target.Container); err != nil { return err }
			if err := appendRewriteTargetUsage(out, "destination", target.Destination); err != nil { return err }
			if target.Attribute == 0 {
				fmt.Fprintf(out, " slice %t", target.Node.Slice)
				for dimension, subscript := range target.Node.Subscripts {
					if err := ctx.Err(); err != nil { return err }
					fmt.Fprintf(out, " {dimension %d slice %t", dimension, subscript.Slice)
					if err := appendRewriteTargetBound(ctx, out, "lower", subscript.Lower); err != nil { return err }
					if err := appendRewriteTargetBound(ctx, out, "upper", subscript.Upper); err != nil { return err }
					out.WriteByte('}')
				}
			}
			out.WriteByte('}')
		}
		out.WriteByte('}')
	}
	return ctx.Err()
}

func appendRewriteTargetUsage(out *strings.Builder, role string, usage sqltype.Usage) error {
	if usage.Type == nil { return fmt.Errorf("stuntgres: missing rewrite assignment usage") }
	fmt.Fprintf(out, " %s type %d typmod %d collation %d derivation %d", role, usage.Type.ID(), usage.Typmod, usage.Collation.ID, usage.Collation.Derivation)
	return nil
}

func appendRewriteTargetBound(ctx context.Context, out *strings.Builder, role string, bound expr.Expr) error {
	if err := ctx.Err(); err != nil { return err }
	out.WriteByte(' ')
	out.WriteString(role)
	out.WriteByte(' ')
	if bound == nil { out.WriteString("<>"); return nil }
	return plan.AppendBoundTree(ctx, out, bound)
}
