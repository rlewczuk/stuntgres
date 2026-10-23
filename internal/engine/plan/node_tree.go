package plan

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// BoundTree is the pg_node_tree carrier of a bound expression.
type BoundTree struct{ Expression expr.Expr }

// StructuralOutput is native structural data, not PostgreSQL nodeToString.
// Captured immutable nodes do not depend on their owner's continued existence.
func (t BoundTree) StructuralOutput(ctx context.Context) (string, error) {
	var out strings.Builder
	err := AppendBoundTree(ctx, &out, t.Expression)
	return out.String(), err
}

// AppendBoundTree writes the structural output of node to out.
func AppendBoundTree(ctx context.Context, out *strings.Builder, node expr.Expr) error {
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
		return AppendBoundTree(ctx, out, n)
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
	case TableOIDExpr:
		fmt.Fprintf(out, "TableOID owner %d", n.Owner.Address.ObjectID)
	case LiteralExpr:
		out.WriteString("Const")
		if n.Value.IsNull() {
			out.WriteString(" null")
			if n.UnknownNull {
				out.WriteString(" unknown")
			}
		} else {
			text, failure := n.Value.Type().Output(ctx, n.Value)
			if failure != nil {
				return failure
			}
			out.WriteString(" value ")
			out.WriteString(strconv.Quote(text))
		}
	case EarthCheckExpr:
		fmt.Fprintf(out, "EarthCheck %d", n.Kind)
	case DomainValueExpr:
		fmt.Fprint(out, "DomainValue")
	case ColumnExpr:
		fmt.Fprintf(out, "Var attnum %d", n.Origin.Address.SubID)
	case RowExpr:
		fmt.Fprint(out, "Row")
		for _, field := range n.Fields {
			if err = child(field); err != nil {
				break
			}
		}
	case FieldExpr:
		fmt.Fprintf(out, "Field attnum %d", n.Attribute)
		err = child(n.Base)
	case ArrayExpr:
		fmt.Fprint(out, "Array")
		for _, element := range n.Elements {
			if err = child(element); err != nil {
				break
			}
		}
	case SubscriptExpr:
		fmt.Fprintf(out, "Subscript slice %t", n.Slice)
		if err = child(n.Base); err != nil {
			return err
		}
		for _, s := range n.Subscripts {
			fmt.Fprintf(out, " Dimension slice %t lower %t upper %t", s.Slice, s.Lower != nil, s.Upper != nil)
			if s.Lower != nil {
				if err = child(s.Lower); err != nil {
					return err
				}
			}
			if s.Upper != nil {
				if err = child(s.Upper); err != nil {
					return err
				}
			}
		}
	case CollationExpr:
		fmt.Fprintf(out, "Collate oid %d", n.Usage.Collation.ID)
		err = child(n.Child)
	case CoercionExpr:
		fmt.Fprintf(out, "Coerce mode %d", n.Mode)
		err = child(n.Child)
	case SelectedOperatorExpr:
		fmt.Fprintf(out, "Operator oid %d", n.Ref.Address.ObjectID)
		err = child(n.Provider)
	case ArithmeticExpr:
		fmt.Fprintf(out, "Arithmetic %d namespace %q", n.Operator, n.Namespace)
		err = pair(n.Left, n.Right)
	case BitwiseExpr:
		fmt.Fprintf(out, "Bitwise %d namespace %q", n.Operator, n.Namespace)
		err = pair(n.Left, n.Right)
	case LikeExpr:
		fmt.Fprintf(out, "Like %s namespace %q", n.Operator, n.Namespace)
		err = pair(n.Left, n.Right)
	case RowComparisonExpr:
		fmt.Fprintf(out, "RowCompare %s", n.Operator)
		for _, comparison := range n.Comparisons {
			if err = pair(comparison.Left, comparison.Right); err != nil {
				break
			}
		}
	case ScalarArrayComparisonExpr:
		fmt.Fprintf(out, "ScalarArrayCompare %s namespace %q all %t", n.Operator, n.Namespace, n.All)
		err = child(n.Left)
		if err == nil && n.Array != nil {
			err = child(n.Array)
		}
		if err == nil {
			err = args(n.Items)
		}
	case ComparisonExpr:
		fmt.Fprintf(out, "Compare %s namespace %q", n.Operator, n.Namespace)
		err = pair(n.Left, n.Right)
	case LogicalExpr:
		fmt.Fprintf(out, "Bool %d", n.Op)
		err = pair(n.Left, n.Right)
	case NullTestExpr:
		fmt.Fprintf(out, "NullTest not %t", n.Not)
		if n.Whole {
			out.WriteString(" whole true")
		}
		err = child(n.Child)
	case TruthTestExpr:
		fmt.Fprintf(out, "TruthTest %d not %t", n.Truth, n.Not)
		err = child(n.Child)
	case DistinctExpr:
		fmt.Fprintf(out, "Distinct not %t", n.Not)
		err = pair(n.Left, n.Right)
	case ConditionalExpr:
		fmt.Fprintf(out, "Conditional %d", n.Kind)
		err = args(n.Args)
	case CaseTestExpr:
		out.WriteString("CaseTest")
	case DatumCastExpr:
		fmt.Fprintf(out, "DatumCast %s %s %d", n.Method, n.Context, n.Mode)
		err = child(n.Child)
	case ArrayCastExpr:
		fmt.Fprintf(out, "ArrayCast %s %d", n.Context, n.Mode)
		err = pair(n.Child, n.Element)
	case ArrayCastElementExpr:
		out.WriteString("ArrayCastElement")
	case CaseExpr:
		if n.Test == nil {
			out.WriteString("Case searched")
		} else {
			out.WriteString("Case simple")
			err = child(n.Test)
		}
		for _, arm := range n.Arms {
			if err != nil {
				return err
			}
			err = pair(arm.Condition, arm.Result)
			if err == nil && arm.Test != nil {
				err = child(arm.Test)
			}
		}
		if err == nil {
			err = child(n.Otherwise)
		}
	case ExtensionPathsExpr:
		if failure := n.Validate(); failure != nil {
			return failure
		}
		out.WriteString("ExtensionPaths")
		err = child(n.Arg)
	case InstalledCubeExpr:
		if n.Procedure == nil {
			return fmt.Errorf("stuntgres: missing cube procedure")
		}
		fmt.Fprintf(out, "InstalledCube %d %s", n.Ref.Address.ObjectID, n.Procedure.Signature)
		err = args(n.Args)
	case InstalledSoundexExpr:
		fmt.Fprintf(out, "InstalledSoundex %d %s", n.Ref.Address.ObjectID, n.Procedure.Signature)
		err = args(n.Args)
	case InstalledMetaphoneExpr:
		fmt.Fprintf(out, "InstalledMetaphone %d %s", n.Ref.Address.ObjectID, n.Procedure.Signature)
		err = args(n.Args)
	case InstalledLevenshteinExpr:
		fmt.Fprintf(out, "InstalledLevenshtein %d %s", n.Ref.Address.ObjectID, n.Procedure.Signature)
		err = args(n.Args)
	case InstalledUUIDOsspExpr:
		fmt.Fprintf(out, "InstalledUUIDOssp %d", n.Ref.Address.ObjectID)
		err = args(n.Args)
	case InstalledCryptoExpr:
		fmt.Fprintf(out, "InstalledCrypto %d %s", n.Ref.Address.ObjectID, n.Procedure.Signature)
		err = args(n.Args)
	case InstalledCubeSpatialExpr:
		fmt.Fprintf(out, "InstalledCubeSpatial %d %s", n.Ref.Address.ObjectID, n.Procedure.Signature)
		err = args(n.Args[:])
	case RangeConstructorExpr:
		fmt.Fprintf(out, "RangeConstructor %d %s", n.Ref.Address.ObjectID, n.Procedure.Signature)
		err = args(n.Args)
	case SQLRoutineExpr:
		fmt.Fprintf(out, "SQLRoutine %d %s", n.Ref.Address.ObjectID, n.Procedure.Signature)
		err = args(n.Args)
	case InstalledTextExpr:
		fmt.Fprintf(out, "InstalledFunction %d", n.Ref.Address.ObjectID)
		err = args([]expr.Expr{n.Arg})
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

// CatalogTree stores expression as a pg_node_tree value of typ.
func CatalogTree(typ *sqltype.Descriptor, expression expr.Expr) (sqltype.Value, error) {
	return sqltype.Tree(typ, BoundTree{expression})
}
