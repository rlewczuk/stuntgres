package functions

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// These nodes retain only their typed operand and routine identity. Topology is
// borrowed from the executing statement's catalog, never retained by a plan.
type PartitionTree struct {
	expr.Info
	Arg expr.Expr
}
type PartitionAncestors struct {
	expr.Info
	Arg expr.Expr
}
type PartitionRoot struct {
	expr.Info
	Arg expr.Expr
}

func bindPartitionInfo(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	var single [1]string
	names := single[:0]
	if len(raw.Args) == 1 {
		names = single[:]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if len(raw.Args) == 1 {
			operand = arg
		}
		if len(raw.Args) <= 100 {
			name := "unknown"
			if arg.Usage.Type != nil {
				name = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if !visible || !partitionInfoArguments(raw) || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	target := b.Type(sqltype.Regclass).Usage()
	// Formal REGCLASS I/O coercions borrow the same captured type-input
	// authority as explicit casts and the existing index-property inquiries.
	if err := b.CaptureTypeInput(target.Type); err != nil {
		return nil, err
	}
	arg, err := b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: target})
	if err != nil {
		return nil, err
	}
	if arg.ResultUsage() != target {
		plan, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
		if failure != nil {
			if diagnostic, ok := failure.(*sqlstate.Error); ok && (diagnostic.Code == "42804" || diagnostic.Code == "42846") {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
			return nil, failure
		}
		arg, err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[0], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
		if err != nil {
			return nil, err
		}
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	result := target
	volatility := expr.Immutable
	if name != "pg_partition_root" {
		if err := b.AdmitSetReturning(raw.Pos); err != nil {
			return nil, err
		}
		volatility = expr.Volatile
	}
	if name == "pg_partition_tree" {
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{
			{Attribute: 1, Name: "relid", Usage: target},
			{Attribute: 2, Name: "parentrelid", Usage: target},
			{Attribute: 3, Name: "isleaf", Usage: b.Type(sqltype.Bool).Usage()},
			{Attribute: 4, Name: "level", Usage: b.Type(sqltype.Int4).Usage()},
		})
		if err != nil {
			return nil, err
		}
		result = shape.Usage()
	}
	properties := arg.Properties()
	properties.Volatility = max(volatility, properties.Volatility)
	info, err := b.BuiltinRoutineInfo(b.Info(result, raw.Pos, properties), CatalogRoutineProvider, name, sqltype.Regclass)
	if err != nil {
		return nil, err
	}
	b.NoteCatalogSnapshot()
	b.NoteCatalogText()
	switch name {
	case "pg_partition_tree":
		return PartitionTree{info, arg}, nil
	case "pg_partition_ancestors":
		return PartitionAncestors{info, arg}, nil
	default:
		return PartitionRoot{info, arg}, nil
	}
}

func validatePartitionInfo(info expr.Info, arg expr.Expr, tree bool) error {
	if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != sqltype.Regclass || info.Usage.Type == nil {
		return fmt.Errorf("stuntgres: invalid partition inquiry signature")
	}
	if !tree {
		if info.Usage.Type.ID() != sqltype.Regclass {
			return fmt.Errorf("stuntgres: invalid partition inquiry result")
		}
		return nil
	}
	typ := info.Usage.Type
	if typ.CompositeLen() != 4 {
		return fmt.Errorf("stuntgres: invalid partition tree result")
	}
	for i, field := range [...]struct {
		name string
		id   sqltype.ID
	}{{"relid", sqltype.Regclass}, {"parentrelid", sqltype.Regclass}, {"isleaf", sqltype.Bool}, {"level", sqltype.Int4}} {
		member := typ.CompositeMember(i)
		if member.Name != field.name || member.Usage.Type == nil || member.Usage.Type.ID() != field.id {
			return fmt.Errorf("stuntgres: invalid partition tree field")
		}
	}
	return nil
}
func (n PartitionTree) Validate() error      { return validatePartitionInfo(n.Info, n.Arg, true) }
func (n PartitionAncestors) Validate() error { return validatePartitionInfo(n.Info, n.Arg, false) }
func (n PartitionRoot) Validate() error      { return validatePartitionInfo(n.Info, n.Arg, false) }

func partitionInfoInput(ctx expr.Context, arg expr.Expr) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	return arg.Eval(ctx)
}
func (n PartitionRoot) Eval(ctx expr.Context) (sqltype.Value, error) {
	value, err := partitionInfoInput(ctx, n.Arg)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	oid, err := partitionInfoOID(value)
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, err := catalogInquiryRuntime(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.PartitionRoot(oid, n.Usage.Type)
}
func (PartitionTree) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: partition tree set used as scalar")
}
func (PartitionAncestors) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: partition ancestors set used as scalar")
}
func (n PartitionTree) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	return openPartitionInfo(ctx, n.Arg, n.Usage.Type, true)
}
func (n PartitionAncestors) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	return openPartitionInfo(ctx, n.Arg, n.Usage.Type, false)
}
func openPartitionInfo(ctx expr.Context, arg expr.Expr, result *sqltype.Descriptor, tree bool) (expr.SourceCursor, error) {
	value, err := partitionInfoInput(ctx, arg)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if value.IsNull() {
		return expr.NewSourceCursor(expr.SourceCursor{Type: result, Exhausted: true}), nil
	}
	oid, err := partitionInfoOID(value)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	runtime, err := catalogInquiryRuntime(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if tree {
		return runtime.PartitionTree(oid, result)
	}
	return runtime.PartitionAncestors(oid, result)
}
func (n PartitionRoot) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
func (PartitionAncestors) ScalarSourceOutName() string { return "relid" }

func partitionInfoOID(value sqltype.Value) (uint32, error) {
	text, err := value.Text()
	if err != nil {
		return 0, err
	}
	oid, err := strconv.ParseUint(text, 10, 32)
	return uint32(oid), err
}

func partitionInfoArguments(raw parser.FunctionExpr) bool {
	if len(raw.Name.Parts) == 0 || len(raw.Args) != 1 || raw.Variadic {
		return false
	}
	label := ""
	switch raw.Name.Parts[len(raw.Name.Parts)-1].Text {
	case "pg_partition_tree":
		label = "rootrelid"
	case "pg_partition_ancestors":
		label = "partitionid"
	case "pg_partition_root":
	default:
		return false
	}
	_, valid := namedSlots(raw, []string{label}, 1)
	return valid
}

// Applicability shares the binder's formal labels, without analyzing children
// or resolving a competing routine a second time.
func namedPartitionInfo(_ CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if len(args) != 1 || !partitionInfoArguments(raw) || !declaredTypes(declared, []sqltype.ID{sqltype.Regclass}) {
		return nil, nil, false
	}
	return namedInputs(args, declared, []int{0})
}
