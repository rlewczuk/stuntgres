package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type VisibilityInquiryKind uint8

const (
	TableVisibility VisibilityInquiryKind = iota
	TypeVisibility
	CollationVisibility
	ConversionVisibility
	CollationVersionInquiry
	DatabaseCollationVersionInquiry
	ImportSystemCollationsInquiry
)

func (k VisibilityInquiryKind) name() string {
	switch k {
	case TableVisibility:
		return "pg_table_is_visible"
	case TypeVisibility:
		return "pg_type_is_visible"
	case CollationVisibility:
		return "pg_collation_is_visible"
	case ConversionVisibility:
		return "pg_conversion_is_visible"
	case CollationVersionInquiry:
		return "pg_collation_actual_version"
	case DatabaseCollationVersionInquiry:
		return "pg_database_collation_actual_version"
	case ImportSystemCollationsInquiry:
		return "pg_import_system_collations"
	}
	return ""
}
func (k VisibilityInquiryKind) Class() ids.OID {
	switch k {
	case TableVisibility:
		return ids.RelationClassID
	case TypeVisibility:
		return ids.TypeClassID
	case CollationVisibility, CollationVersionInquiry:
		return ids.CollationClassID
	case ConversionVisibility:
		return ids.ConversionClassID
	case DatabaseCollationVersionInquiry:
		return ids.DatabaseClassID
	case ImportSystemCollationsInquiry:
		return ids.NamespaceClassID
	}
	return 0
}

func (k VisibilityInquiryKind) formal() sqltype.ID {
	if k == ImportSystemCollationsInquiry {
		return sqltype.Regnamespace
	}
	return sqltype.Oid
}

func (k VisibilityInquiryKind) result() sqltype.ID {
	switch k {
	case CollationVersionInquiry, DatabaseCollationVersionInquiry:
		return sqltype.Text
	case ImportSystemCollationsInquiry:
		return sqltype.Int4
	}
	return sqltype.Bool
}

func (k VisibilityInquiryKind) volatility() expr.Volatility {
	if k == CollationVersionInquiry || k == DatabaseCollationVersionInquiry || k == ImportSystemCollationsInquiry {
		return expr.Volatile
	}
	return expr.Stable
}

type VisibilityInquiry struct {
	expr.Info
	Kind VisibilityInquiryKind
	Arg  expr.Expr
}

func bindVisibilityInquiry(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	n, err := BindVisibilityInquiryStructure(b, raw)
	if err != nil {
		return nil, err
	}
	if literal, ok := b.ConstantValue(n.Arg); ok && literal.IsNull() {
		typ := n.Usage.Type
		return b.Literal(sqltype.Null(typ), raw.Pos), nil
	}
	b.NoteVisibilitySnapshot()
	return n, nil
}
func BindVisibilityInquiryStructure(b Binder, raw parser.FunctionExpr) (VisibilityInquiry, error) {
	if b.DefaultConsumer() {
		return VisibilityInquiry{}, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	// Transform children before either the argument guard or namespace lookup.
	// Unknown literals/parameters retain the existing operand inference seam;
	// only the resolved signature supplies their OID input contract.
	var operand Argument
	var singleName [1]string
	names := singleName[:]
	if len(raw.Args) != 1 && len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return VisibilityInquiry{}, err
		}
		if len(raw.Args) == 1 {
			operand = arg
		}
		if len(raw.Args) <= 100 {
			names[i] = "unknown"
			if typ := arg.Usage.Type; typ != nil {
				names[i] = typ.DiagnosticName()
			}
		}
	}
	if len(raw.Args) > 100 {
		return VisibilityInquiry{}, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return VisibilityInquiry{}, err
	}
	if !visible || len(raw.Args) != 1 {
		return VisibilityInquiry{}, b.SignatureError(raw.Name, raw.Pos, names)
	}
	var kind VisibilityInquiryKind
	switch raw.Name.Parts[len(raw.Name.Parts)-1].Text {
	case "pg_table_is_visible":
		kind = TableVisibility
	case "pg_type_is_visible":
		kind = TypeVisibility
	case "pg_collation_is_visible":
		kind = CollationVisibility
	case "pg_conversion_is_visible":
		kind = ConversionVisibility
	case "pg_collation_actual_version":
		kind = CollationVersionInquiry
	case "pg_database_collation_actual_version":
		kind = DatabaseCollationVersionInquiry
	case "pg_import_system_collations":
		kind = ImportSystemCollationsInquiry
	default:
		return VisibilityInquiry{}, fmt.Errorf("stuntgres: invalid visibility inquiry")
	}
	target := b.Type(kind.formal()).Usage()
	arg, err := b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: target})
	if err != nil {
		return VisibilityInquiry{}, err
	}
	if arg.ResultUsage() != target {
		plan, err := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && (diagnostic.Code == "42804" || diagnostic.Code == "42846") {
				return VisibilityInquiry{}, b.SignatureError(raw.Name, raw.Pos, names)
			}
			return VisibilityInquiry{}, err
		}
		arg, err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[0], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
		if err != nil {
			return VisibilityInquiry{}, err
		}
	}
	properties := arg.Properties()
	properties.Volatility = max(kind.volatility(), properties.Volatility)
	result := b.Type(kind.result()).Usage()
	info, err := b.BuiltinRoutineInfo(b.Info(result, raw.Pos, properties), CatalogRoutineProvider, kind.name(), kind.formal())
	if err != nil {
		return VisibilityInquiry{}, err
	}
	return VisibilityInquiry{info, kind, arg}, nil
}
func (e VisibilityInquiry) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := ctx
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.Kind.Class() == 0 || e.Arg == nil || e.Usage.Type == nil || e.Usage.Type.ID() != e.Kind.result() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid visibility inquiry")
	}
	value, err := e.Arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	runtime, err := catalogInquiryRuntime(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.VisibilityInquiry(e.Kind, value, e.Usage.Type)
}
