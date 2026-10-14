package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
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
	case tableOIDExpression:
		fmt.Fprintf(out, "TableOID owner %d", n.owner.Address.ObjectID)
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
	case identityExpression:
		if err := validateDeparseIdentity(n); err != nil {
			return err
		}
		fmt.Fprintf(out, "Identity kind %d ordinaryCall %t", n.kind, n.ordinaryCall)
	case currentDateTimeExpression:
		if err := validateCurrentDateTime(n); err != nil {
			return err
		}
		fmt.Fprintf(out, "CurrentDateTime kind %d hasPrecision %t precision %d", n.kind, n.hasPrecision, n.precision)
	case transactionTimeExpression:
		if err := validateTransactionTime(n); err != nil {
			return err
		}
		fmt.Fprintf(out, "TransactionTime kind %d", n.kind)
	case domainValueExpression:
		fmt.Fprint(out, "DomainValue")
	case columnExpression:
		fmt.Fprintf(out, "Var attnum %d", n.origin.Address.SubID)
	case rowExpression:
		fmt.Fprint(out, "Row")
		for _, field := range n.fields {
			if err = child(field); err != nil {
				break
			}
		}
	case fieldExpression:
		fmt.Fprintf(out, "Field attnum %d", n.attribute)
		err = child(n.base)
	case arrayExpression:
		fmt.Fprint(out, "Array")
		for _, element := range n.elements {
			if err = child(element); err != nil {
				break
			}
		}
	case subscriptExpression:
		fmt.Fprintf(out, "Subscript slice %t", n.slice)
		if err = child(n.base); err != nil {
			return err
		}
		for _, s := range n.subscripts {
			fmt.Fprintf(out, " Dimension slice %t lower %t upper %t", s.slice, s.lower != nil, s.upper != nil)
			if s.lower != nil {
				if err = child(s.lower); err != nil {
					return err
				}
			}
			if s.upper != nil {
				if err = child(s.upper); err != nil {
					return err
				}
			}
		}
	case collationExpression:
		fmt.Fprintf(out, "Collate oid %d", n.usage.Collation.ID)
		err = child(n.child)
	case coercionExpression:
		fmt.Fprintf(out, "Coerce mode %d", n.mode)
		err = child(n.child)
	case arithmeticExpression:
		fmt.Fprintf(out, "Arithmetic %d namespace %q", n.operator, n.namespace)
		err = pair(n.left, n.right)
	case bitwiseExpression:
		fmt.Fprintf(out, "Bitwise %d namespace %q", n.operator, n.namespace)
		err = pair(n.left, n.right)
	case likeExpression:
		fmt.Fprintf(out, "Like %s namespace %q", n.operator, n.namespace)
		err = pair(n.left, n.right)
	case likeEscapeExpression:
		fmt.Fprintf(out, "LikeEscape")
		err = pair(n.pattern, n.escape)
	case posixExpression:
		fmt.Fprintf(out, "Posix %s namespace %q", n.operator, n.namespace)
		err = pair(n.left, n.right)
	case similarExpression:
		fmt.Fprintf(out, "Similar %s", n.operator)
		err = pair(n.left, n.right)
	case similarEscapeExpression:
		fmt.Fprintf(out, "SimilarEscape omitted %t", n.omitted)
		err = pair(n.pattern, n.escape)
	case posixSubstringExpression:
		fmt.Fprintf(out, "PosixSubstring syntax %d", n.syntax)
		err = args(n.args[:])
	case similarSubstringExpression:
		fmt.Fprintf(out, "SimilarSubstring syntax %d", n.syntax)
		err = args(n.args[:])
	case rowComparisonExpression:
		fmt.Fprintf(out, "RowCompare %s", n.operator)
		for _, comparison := range n.comparisons {
			if err = pair(comparison.left, comparison.right); err != nil {
				break
			}
		}
	case regexpExpression:
		fmt.Fprintf(out, "Regexp %s", n.plan.Name())
		err = args(n.args)
	case regexpSetExpression:
		fmt.Fprintf(out, "RegexpSet %s", n.plan.Name())
		err = args(n.args)
	case scalarArrayComparisonExpression:
		fmt.Fprintf(out, "ScalarArrayCompare %s namespace %q all %t", n.operator, n.namespace, n.all)
		err = child(n.left)
		if err == nil && n.array != nil {
			err = child(n.array)
		}
		if err == nil {
			err = args(n.items)
		}
	case jsonKeysExpression:
		fmt.Fprintf(out, "JSONKeys %s", n.plan.Name())
		err = args(n.args)
	case seriesExpression:
		fmt.Fprint(out, "Series generate_series")
		err = args(n.args)
	case subscriptsExpression:
		fmt.Fprint(out, "Subscripts generate_subscripts")
		err = args(n.args)
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
	case arraySearchExpression:
		out.WriteString("ArraySearch " + n.plan.Name() + " namespace " + n.namespace)
		err = args(n.args)
	case enumFunctionExpression:
		out.WriteString("Enum " + n.plan.Name())
		err = args(n.args)
	case mathExpression:
		out.WriteString("Math " + n.plan.Name())
		err = args(n.args)
	case datetimeJustifyExpression:
		out.WriteString("Datetime " + n.plan.Name())
		err = args(n.children())
	case datetimeExtractExpression:
		out.WriteString("Datetime extraction " + n.plan.Name())
		err = args(n.children())
	case datetimeOverlapsExpression:
		out.WriteString("Datetime overlaps")
		err = args(n.children())
	case datetimeBinExpression:
		out.WriteString("Datetime binning")
		err = args(n.children())
	case datetimeTruncExpression:
		out.WriteString("Datetime truncation")
		err = args(n.children())
	case datetimeZoneCalendarExpression:
		out.WriteString("Datetime " + n.name)
		err = args(n.children())
	case datetimeAgeFiniteExpression:
		out.WriteString("Datetime " + n.plan.Name())
		err = args(n.children())
	case datetimeMakeExpression:
		out.WriteString("Datetime constructor " + n.plan.Name())
		err = args(n.children())
	case datetimeFormattingExpression:
		out.WriteString("Datetime " + n.plan.Name())
		err = args(n.args[:])
	case numericFormattingExpression:
		out.WriteString("NumericFormatting " + n.plan.Name())
		err = args(n.args[:])
	case stringExpression:
		out.WriteString("String " + n.plan.Name())
		err = args(n.args)
	case binaryExpression:
		if err := n.validateSyntax(); err != nil {
			return err
		}
		out.WriteString("Binary ")
		out.WriteString(n.plan.Name())
		if n.syntax == parser.SQLFunctionSyntax && (n.plan.Name() == "btrim" || n.plan.Name() == "ltrim" || n.plan.Name() == "rtrim") {
			out.WriteString(" syntax TRIM")
		}
		err = args(n.args)
	case randomExpression:
		out.WriteString("Random " + n.name)
		err = args(n.args)
	case datetimeSleepExpression:
		out.WriteString("DatetimeSleep " + datetimeSleepName(n.kind))
		err = args([]BoundExpr{n.arg})
	case advisoryExpression:
		out.WriteString("AdvisorySession " + n.name)
		err = args(n.args)
	case uuidGenerationExpression:
		out.WriteString("UUIDGeneration " + n.name)
		err = args(n.args)
	case nullCountExpression:
		fmt.Fprintf(out, "NullCount %d", n.kind)
		err = args(n.args)
	case installedFunctionExpression:
		fmt.Fprintf(out, "InstalledFunction %d", n.ref.Address.ObjectID)
		err = args([]BoundExpr{n.arg})
	case jsonCreateExpression:
		fmt.Fprintf(out, "JSONCreate %s variadic %t", n.kind.Name(), n.variadic)
		err = args(n.args)
	case stringVariadicExpression:
		fmt.Fprintf(out, "StringVariadic %s variadic %t anyelement %t", n.name, n.variadic, n.anyelement)
		err = args(n.args)
	case formatExpression:
		fmt.Fprintf(out, "Format variadic %t", n.variadic)
		err = args(n.args)
	case visibilityInquiryExpression:
		if n.kind.class() == 0 || u != u.Type.Usage() || u.Type.ID() != sqltype.Bool || n.arg == nil {
			return fmt.Errorf("stuntgres: invalid stored visibility inquiry signature")
		}
		argument := n.arg.ResultUsage()
		if argument.Type == nil || argument != argument.Type.Usage() || argument.Type.ID() != sqltype.Oid {
			return fmt.Errorf("stuntgres: invalid stored visibility inquiry argument")
		}
		properties := n.arg.Properties()
		properties.Volatility = max(Stable, properties.Volatility)
		if n.Properties() != properties {
			return fmt.Errorf("stuntgres: invalid stored visibility inquiry properties")
		}
		fmt.Fprintf(out, "VisibilityInquiry %d", n.kind)
		err = child(n.arg)
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
		if n.function {
			out.WriteString("Datetime timezone")
			err = args(n.functionArguments())
			break
		}
		if n.local {
			out.WriteString("AtLocal")
			err = child(n.value)
		} else {
			out.WriteString("AtTimeZone")
			err = pair(n.value, n.zone)
		}
	case indexPropertyExpression:
		if err := validateStoredIndexPropertyExpression(n); err != nil {
			return err
		}
		fmt.Fprintf(out, "IndexProperty kind %d", n.kind)
		err = args(n.children())
	case deparseExpression:
		fmt.Fprintf(out, "Deparse signature %d", n.signature)
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
