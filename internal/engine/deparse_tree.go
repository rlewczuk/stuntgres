package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/sqltype"
)

type boundTree struct{ expression BoundExpr }

// StructuralOutput is native structural data, not PostgreSQL nodeToString.
// Captured immutable nodes do not depend on their owner's continued existence.
func (t boundTree) StructuralOutput(ctx context.Context) (string, error) {
	var out strings.Builder
	err := appendBoundTree(ctx, &out, t.expression)
	return out.String(), err
}

func appendBoundTree(ctx context.Context, out *strings.Builder, node BoundExpr) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if node == nil || node.ResultUsage().Type == nil {
		return fmt.Errorf("stuntgres: missing structural node")
	}
	u := node.ResultUsage()
	fmt.Fprintf(out, "{type %d typmod %d collation %d derivation %d node ", u.Type.ID(), u.Typmod, u.Collation.ID, u.Collation.Derivation)
	child := func(n BoundExpr) error {
		if n == nil {
			return nil
		}
		out.WriteByte(' ')
		return appendBoundTree(ctx, out, n)
	}
	pair := func(a, b BoundExpr) error {
		if err := child(a); err != nil {
			return err
		}
		return child(b)
	}
	args := func(nodes []BoundExpr) error {
		for _, n := range nodes {
			if err := child(n); err != nil {
				return err
			}
		}
		return nil
	}
	var err error
	switch n := node.(type) {
	case literalExpression:
		out.WriteString("Const")
		if n.value.IsNull() {
			out.WriteString(" null")
			if n.unknownNull {
				out.WriteString(" unknown")
			}
		} else {
			text, failure := n.value.Type().Output(ctx, n.value)
			if failure != nil {
				return failure
			}
			out.WriteString(" value ")
			out.WriteString(strconv.Quote(text))
		}
	case columnExpression:
		fmt.Fprintf(out, "Var attnum %d", n.origin.Address.SubID)
	case coercionExpression:
		fmt.Fprintf(out, "Coerce mode %d", n.mode)
		err = child(n.child)
	case arithmeticExpression:
		fmt.Fprintf(out, "Arithmetic %d namespace %q", n.operator, n.namespace)
		err = pair(n.left, n.right)
	case bitwiseExpression:
		fmt.Fprintf(out, "Bitwise %d namespace %q", n.operator, n.namespace)
		err = pair(n.left, n.right)
	case comparisonExpression:
		fmt.Fprintf(out, "Compare %s namespace %q", n.operator, n.namespace)
		err = pair(n.left, n.right)
	case logicalExpression:
		fmt.Fprintf(out, "Bool %d", n.op)
		err = pair(n.left, n.right)
	case nullTestExpression:
		fmt.Fprintf(out, "NullTest not %t", n.not)
		err = child(n.child)
	case truthTestExpression:
		fmt.Fprintf(out, "TruthTest %d not %t", n.truth, n.not)
		err = child(n.child)
	case distinctExpression:
		fmt.Fprintf(out, "Distinct not %t", n.not)
		err = pair(n.left, n.right)
	case conditionalExpression:
		fmt.Fprintf(out, "Conditional %d", n.kind)
		err = args(n.args)
	case caseExpression:
		if n.test == nil {
			out.WriteString("Case searched")
		} else {
			out.WriteString("Case simple")
			err = child(n.test)
		}
		for _, arm := range n.arms {
			if err != nil {
				return err
			}
			err = pair(arm.condition, arm.result)
		}
		if err == nil {
			err = child(n.otherwise)
		}
	case mathExpression:
		out.WriteString("Math " + n.plan.Name())
		err = args(n.args)
	case randomExpression:
		out.WriteString("Random " + n.name)
		err = args(n.args)
	case nullCountExpression:
		fmt.Fprintf(out, "NullCount %d", n.kind)
		err = args(n.args)
	case commentInquiryExpression:
		fmt.Fprintf(out, "CommentInquiry %d", n.kind)
		err = args(n.args)
	case currentSchemasExpression:
		out.WriteString("CurrentSchemas")
		err = child(n.arg)
	case sequenceCallExpression:
		fmt.Fprintf(out, "Sequence %s oid %d", n.name, n.ref.Address.ObjectID)
		err = args(n.args)
	case timeZoneExpression:
		if n.local {
			out.WriteString("AtLocal")
			err = child(n.value)
		} else {
			out.WriteString("AtTimeZone")
			err = pair(n.value, n.zone)
		}
	case deparseExpression:
		fmt.Fprintf(out, "Deparse constraint %t", n.constraint)
		err = args(n.args)
	default:
		return fmt.Errorf("stuntgres: unsupported structural node %T", node)
	}
	if err != nil {
		return err
	}
	out.WriteByte('}')
	return nil
}

func catalogTree(typ *sqltype.Descriptor, expression BoundExpr) (sqltype.Value, error) {
	return sqltype.Tree(typ, boundTree{expression})
}
