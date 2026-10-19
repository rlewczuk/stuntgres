package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type boundTree struct{ expression expr.Expr }

// StructuralOutput is native structural data, not PostgreSQL nodeToString.
// Captured immutable nodes do not depend on their owner's continued existence.
func (t boundTree) StructuralOutput(ctx context.Context) (string, error) {
	var out strings.Builder
	err := appendBoundTree(ctx, &out, t.expression)
	return out.String(), err
}

func appendBoundTree(ctx context.Context, out *strings.Builder, node expr.Expr) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if node == nil || node.ResultUsage().Type == nil {
		return fmt.Errorf("stuntgres: missing structural node")
	}
	u := node.ResultUsage()
	fmt.Fprintf(out, "{type %d typmod %d collation %d derivation %d node ", u.Type.ID(), u.Typmod, u.Collation.ID, u.Collation.Derivation)
	child := func(n expr.Expr) error {
		if n == nil {
			return nil
		}
		out.WriteByte(' ')
		return appendBoundTree(ctx, out, n)
	}
	pair := func(a, b expr.Expr) error {
		if err := child(a); err != nil {
			return err
		}
		return child(b)
	}
	args := func(nodes []expr.Expr) error {
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
			if n.UnknownNull {
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
		fmt.Fprintf(out, "Collate oid %d", n.Usage.Collation.ID)
		err = child(n.child)
	case coercionExpression:
		fmt.Fprintf(out, "Coerce mode %d", n.mode)
		err = child(n.child)
	case selectedOperatorExpression:
		fmt.Fprintf(out, "Operator oid %d", n.ref.Address.ObjectID)
		err = child(n.provider)
	case arithmeticExpression:
		fmt.Fprintf(out, "Arithmetic %d namespace %q", n.operator, n.namespace)
		err = pair(n.left, n.right)
	case bitwiseExpression:
		fmt.Fprintf(out, "Bitwise %d namespace %q", n.operator, n.namespace)
		err = pair(n.left, n.right)
	case likeExpression:
		fmt.Fprintf(out, "Like %s namespace %q", n.operator, n.namespace)
		err = pair(n.left, n.right)
	case rowComparisonExpression:
		fmt.Fprintf(out, "RowCompare %s", n.operator)
		for _, comparison := range n.comparisons {
			if err = pair(comparison.left, comparison.right); err != nil {
				break
			}
		}
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
		if n.procedure != nil {
			fmt.Fprintf(out, "Intagg %s", n.procedure.Name())
		} else {
			fmt.Fprint(out, "Unnest")
		}
		err = args(n.args)
	case comparisonExpression:
		fmt.Fprintf(out, "Compare %s namespace %q", n.operator, n.namespace)
		err = pair(n.left, n.right)
	case logicalExpression:
		fmt.Fprintf(out, "Bool %d", n.op)
		err = pair(n.left, n.right)
	case nullTestExpression:
		fmt.Fprintf(out, "NullTest not %t", n.not)
		if n.whole {
			out.WriteString(" whole true")
		}
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
	case catalogUserNameExpression:
		if failure := n.validate(); failure != nil {
			return failure
		}
		out.WriteString("CatalogUserName")
		err = child(n.arg)
	case catalogForeignKeysExpression:
		if failure := n.validate(); failure != nil {
			return failure
		}
		out.WriteString("CatalogForeignKeys")
	case extensionPathsExpression:
		if failure := n.validate(); failure != nil {
			return failure
		}
		out.WriteString("ExtensionPaths")
		err = child(n.arg)
	case transactionIdentityExpression:
		out.WriteString("TransactionIdentity " + n.kind.name())
		if n.arg != nil {
			err = child(n.arg)
		}
	case commitTimestampExpression:
		out.WriteString("CommitTimestamp " + n.kind.name())
		if n.arg != nil {
			err = child(n.arg)
		}
	case advisoryExpression:
		if n.lifetime == advisoryTransaction {
			out.WriteString("AdvisoryTransaction " + n.name)
		} else {
			out.WriteString("AdvisorySession " + n.name)
		}
		err = args(n.args)
	case installedSoundexFunctionExpression:
		fmt.Fprintf(out, "InstalledSoundex %d %s", n.ref.Address.ObjectID, n.procedure.Signature)
		err = args(n.Args)
	case installedMetaphoneFunctionExpression:
		fmt.Fprintf(out, "InstalledMetaphone %d %s", n.ref.Address.ObjectID, n.procedure.Signature)
		err = args(n.Args)
	case installedLevenshteinFunctionExpression:
		fmt.Fprintf(out, "InstalledLevenshtein %d %s", n.ref.Address.ObjectID, n.procedure.Signature)
		err = args(n.Args)
	case installedUUIDOsspFunctionExpression:
		fmt.Fprintf(out, "InstalledUUIDOssp %d", n.ref.Address.ObjectID)
	case installedCryptoFunctionExpression:
		fmt.Fprintf(out, "InstalledCrypto %d %s", n.ref.Address.ObjectID, n.procedure.Signature)
		err = args(n.Args)
	case installedFunctionExpression:
		fmt.Fprintf(out, "InstalledFunction %d", n.ref.Address.ObjectID)
		err = args([]expr.Expr{n.Arg})
	case visibilityInquiryExpression:
		if n.kind.class() == 0 || u.Type == nil || u != u.Type.Usage() || u.Type.ID() != n.kind.result() || n.arg == nil {
			return fmt.Errorf("stuntgres: invalid stored visibility inquiry signature")
		}
		argument := n.arg.ResultUsage()
		if argument.Type == nil || argument != argument.Type.Usage() || argument.Type.ID() != n.kind.formal() {
			return fmt.Errorf("stuntgres: invalid stored visibility inquiry argument")
		}
		properties := n.arg.Properties()
		properties.Volatility = max(n.kind.volatility(), properties.Volatility)
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
	case objectAddressExpression:
		if err := n.validateStored(); err != nil {
			return err
		}
		fmt.Fprintf(out, "ObjectAddress %d slots %v labels %v", n.op, n.slots, n.labels)
		err = args(n.args[:])
	case namespaceInquiryExpression:
		if err := n.validateStored(); err != nil {
			return err
		}
		fmt.Fprintf(out, "NamespaceInquiry %d", n.kind)
		err = args(n.arguments())
	case blockingInquiryExpression:
		if err := n.validateStored(); err != nil {
			return err
		}
		fmt.Fprintf(out, "BlockingInquiry %d", n.kind)
		err = child(n.arg)
	case sequenceCallExpression:
		fmt.Fprintf(out, "Sequence %s oid %d", n.name, n.ref.Address.ObjectID)
		err = args(n.args)
	case indexPropertyExpression:
		if err := validateStoredIndexPropertyExpression(n); err != nil {
			return err
		}
		fmt.Fprintf(out, "IndexProperty kind %d", n.kind)
		err = args(n.children())
	case deparseExpression:
		fmt.Fprintf(out, "Deparse signature %d", n.signature)
		err = args(n.args)
	case expr.Renderable:
		// Function nodes describe themselves; engine wrappers that embed one
		// keep their explicit cases above.
		spec, failure := n.Render()
		if failure != nil {
			return failure
		}
		if spec.Tree == "" {
			return fmt.Errorf("stuntgres: unsupported structural node %T", node)
		}
		out.WriteString(spec.Tree)
		err = args(spec.Inputs)
	default:
		return fmt.Errorf("stuntgres: unsupported structural node %T", node)
	}
	if err != nil {
		return err
	}
	out.WriteByte('}')
	return nil
}

func catalogTree(typ *sqltype.Descriptor, expression expr.Expr) (sqltype.Value, error) {
	return sqltype.Tree(typ, boundTree{expression})
}
