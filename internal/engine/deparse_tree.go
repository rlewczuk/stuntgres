package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/functions"
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
	case functions.CurrentDateTime[*EvalContext, ExprProperties]:
		if err := n.Validate(functionPropertiesMatch(n.Property, functions.CurrentDateTimeIntrinsicRule())); err != nil {
			return err
		}
		fmt.Fprintf(out, "CurrentDateTime kind %d hasPrecision %t precision %d", n.Kind, n.HasPrecision, n.Precision)
	case functions.TransactionTime[*EvalContext, ExprProperties]:
		if err := n.Validate(functionPropertiesMatch(n.Property, n.Kind.IntrinsicRule())); err != nil {
			return err
		}
		fmt.Fprintf(out, "TransactionTime kind %d", n.Kind)
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
	case functions.LikeEscape[BoundExpr, *EvalContext, ExprProperties]:
		fmt.Fprintf(out, "LikeEscape")
		err = pair(n.Pattern, n.Escape)
	case functions.Posix[BoundExpr, *EvalContext, ExprProperties]:
		fmt.Fprintf(out, "Posix %s namespace %q", n.Operator, n.Namespace)
		err = pair(n.Left, n.Right)
	case functions.Similar[BoundExpr, *EvalContext, ExprProperties]:
		fmt.Fprintf(out, "Similar %s", n.Operator)
		err = pair(n.Left, n.Right)
	case functions.SimilarEscape[BoundExpr, *EvalContext, ExprProperties]:
		fmt.Fprintf(out, "SimilarEscape omitted %t", n.Omitted)
		err = pair(n.Pattern, n.Escape)
	case functions.PosixSubstring[BoundExpr, *EvalContext, ExprProperties]:
		fmt.Fprintf(out, "PosixSubstring syntax %d", n.Syntax)
		err = args(n.Args[:])
	case functions.SimilarSubstring[BoundExpr, *EvalContext, ExprProperties]:
		fmt.Fprintf(out, "SimilarSubstring syntax %d", n.Syntax)
		err = args(n.Args[:])
	case rowComparisonExpression:
		fmt.Fprintf(out, "RowCompare %s", n.operator)
		for _, comparison := range n.comparisons {
			if err = pair(comparison.left, comparison.right); err != nil {
				break
			}
		}
	case functions.Regexp[BoundExpr, *EvalContext, ExprProperties]:
		fmt.Fprintf(out, "Regexp %s", n.Plan.Name())
		err = args(n.Args)
	case functions.RegexpSet[BoundExpr, *EvalContext, ExprProperties]:
		fmt.Fprintf(out, "RegexpSet %s", n.Plan.Name())
		err = args(n.Args)
	case scalarArrayComparisonExpression:
		fmt.Fprintf(out, "ScalarArrayCompare %s namespace %q all %t", n.operator, n.namespace, n.all)
		err = child(n.left)
		if err == nil && n.array != nil {
			err = child(n.array)
		}
		if err == nil {
			err = args(n.items)
		}
	case seriesExpression:
		fmt.Fprint(out, "Series generate_series")
		err = args(n.args)
	case subscriptsExpression:
		fmt.Fprint(out, "Subscripts generate_subscripts")
		err = args(n.args)
	case unnestExpression:
		fmt.Fprint(out, "Unnest")
		err = args(n.args)
	case functions.JSONKeys[BoundExpr, *EvalContext, ExprProperties]:
		fmt.Fprintf(out, "JSONKeys %s", n.Plan.Name())
		err = args(n.Args)
	case functions.JSONBElements[BoundExpr, *EvalContext, ExprProperties]:
		fmt.Fprintf(out, "JSONBElements %s", n.Plan.Name())
		err = args(n.Args)
	case functions.JSONEach[BoundExpr, *EvalContext, ExprProperties]:
		fmt.Fprintf(out, "JSONEach %s", n.Plan.Name())
		err = args(n.Args)
	case functions.JSONElements[BoundExpr, *EvalContext, ExprProperties]:
		fmt.Fprintf(out, "JSONElements %s", n.Plan.Name())
		err = args(n.Args)
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
	case functions.ArrayRandom[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("ArrayRandom " + n.Kind.Name() + " namespace " + n.Namespace)
		err = args(n.Args)
	case functions.ArrayInspect[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("ArrayInspect " + n.Kind.Name() + " namespace " + n.Namespace)
		err = args(n.Args)
	case functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("ArraySearch " + n.Plan.Name() + " namespace " + n.Namespace)
		err = args(n.Args)
	case functions.UUIDExtract[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("UUIDExtract " + n.Kind.Name())
		err = child(n.Arg)
	case functions.RangeInspect[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("RangeInspect " + n.Plan.Name())
		err = args(n.Args)
	case functions.Enum[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("Enum " + n.Plan.Name())
		err = args(n.Args)
	case functions.Math[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("Math " + n.Plan.Name())
		err = args(n.Args)
	case functions.DatetimeJustify[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("Datetime " + n.Plan.Name())
		err = args(n.Arguments())
	case functions.DatetimeExtract[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("Datetime extraction " + n.Plan.Name())
		err = args(n.Arguments())
	case functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("Datetime overlaps")
		err = args(n.Arguments())
	case functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("Datetime binning")
		err = args(n.Arguments())
	case functions.DatetimeTrunc[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("Datetime truncation")
		err = args(n.Arguments())
	case functions.DatetimeZoneCalendar[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("Datetime " + n.Name)
		err = args(n.Arguments())
	case functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("Datetime " + n.Plan.Name())
		err = args(n.Arguments())
	case functions.JSONStripNulls[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("JSON stripping")
		err = args(n.Arguments())
	case functions.JSONExtract[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("JSON extraction " + n.Plan.Name())
		err = args(n.Arguments())
	case functions.JSONBSet[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("JSONB replacement " + n.Plan.Name())
		err = args(n.Arguments())
	case functions.DatetimeMake[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("Datetime constructor " + n.Plan.Name())
		err = args(n.Arguments())
	case functions.DatetimeFormatting[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("Datetime " + n.Plan.Name())
		err = args(n.Args[:])
	case functions.NumericFormatting[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("NumericFormatting " + n.Plan.Name())
		err = args(n.Args[:])
	case functions.String[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("String " + n.Plan.Name())
		err = args(n.Args)
	case functions.Binary[BoundExpr, *EvalContext, ExprProperties]:
		if err := n.ValidateSyntax(); err != nil {
			return err
		}
		out.WriteString("Binary ")
		out.WriteString(n.Plan.Name())
		if n.Syntax == parser.SQLFunctionSyntax && (n.Plan.Name() == "btrim" || n.Plan.Name() == "ltrim" || n.Plan.Name() == "rtrim") {
			out.WriteString(" syntax TRIM")
		}
		err = args(n.Args)
	case functions.Random[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("Random " + n.Name)
		err = args(n.Args)
	case functions.DatetimeSleep[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("DatetimeSleep " + n.Kind.Name())
		err = args([]BoundExpr{n.Arg})
	case advisoryExpression:
		out.WriteString("AdvisorySession " + n.name)
		err = args(n.args)
	case functions.UUIDGeneration[BoundExpr, *EvalContext, ExprProperties]:
		out.WriteString("UUIDGeneration " + n.Name)
		err = args(n.Args)
	case functions.NullCount[BoundExpr, *EvalContext, ExprProperties]:
		fmt.Fprintf(out, "NullCount %d", n.Kind)
		err = args(n.Args)
	case installedSoundexFunctionExpression:
		fmt.Fprintf(out, "InstalledSoundex %d %s", n.ref.Address.ObjectID, n.procedure.signature)
		err = args(n.Args)
	case installedMetaphoneFunctionExpression:
		fmt.Fprintf(out, "InstalledMetaphone %d %s", n.ref.Address.ObjectID, n.procedure.signature)
		err = args(n.Args)
	case installedLevenshteinFunctionExpression:
		fmt.Fprintf(out, "InstalledLevenshtein %d %s", n.ref.Address.ObjectID, n.procedure.signature)
		err = args(n.Args)
	case installedCryptoFunctionExpression:
		fmt.Fprintf(out, "InstalledCrypto %d %s", n.ref.Address.ObjectID, n.procedure.signature)
		err = args(n.Args)
	case installedFunctionExpression:
		fmt.Fprintf(out, "InstalledFunction %d", n.ref.Address.ObjectID)
		err = args([]BoundExpr{n.arg})
	case sqlJSONExpression:
		fmt.Fprintf(out, "SQLJSON %s absent %t unique %t", n.raw.Kind, n.raw.Absent, n.raw.Unique)
		err = args(n.args)
	case jsonPredicateExpression:
		fmt.Fprintf(out, "IS JSON %s unique %t not %t", n.kind, n.unique, n.not)
		err = args([]BoundExpr{n.child})
	case functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties]:
		fmt.Fprintf(out, "JSONCreate %s variadic %t", n.Kind.Name(), n.Variadic)
		err = args(n.Args)
	case functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]:
		fmt.Fprintf(out, "StringVariadic %s variadic %t anyelement %t", n.Name, n.Variadic, n.AnyElement)
		err = args(n.Args)
	case functions.Format[BoundExpr, *EvalContext, ExprProperties]:
		fmt.Fprintf(out, "Format variadic %t", n.Variadic)
		err = args(n.Args)
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
	case objectACLExpression:
		if err := n.validateStored(); err != nil {
			return err
		}
		fmt.Fprintf(out, "ObjectACL slots %v labels %v", n.slots, n.labels)
		err = args(n.args[:])
	case accessInquiryExpression:
		if err := n.validateStored(); err != nil {
			return err
		}
		fmt.Fprintf(out, "AccessInquiry %s", n.name())
		err = args(n.arguments())
	case namespaceInquiryExpression:
		if err := n.validateStored(); err != nil {
			return err
		}
		fmt.Fprintf(out, "NamespaceInquiry %d", n.kind)
		err = args(n.arguments())
	case sequenceCallExpression:
		fmt.Fprintf(out, "Sequence %s oid %d", n.name, n.ref.Address.ObjectID)
		err = args(n.args)
	case functions.Timezone[BoundExpr, *EvalContext, ExprProperties]:
		if n.Function {
			out.WriteString("Datetime timezone")
			err = args(n.Arguments())
			break
		}
		if n.Local {
			out.WriteString("AtLocal")
			err = child(n.Value)
		} else {
			out.WriteString("AtTimeZone")
			err = pair(n.Value, n.Zone)
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
