package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// SizeKind selects a released size operation, not storage or authority.
type SizeKind uint8

const (
	RelationSize SizeKind = iota
	TableSize
	IndexesSize
	TotalRelationSize
	DatabaseSize
	TablespaceSize
)

var sizeNames = [...]string{"pg_relation_size", "pg_table_size", "pg_indexes_size", "pg_total_relation_size", "pg_database_size", "pg_tablespace_size"}

// SizeInquiry retains only typed children and the selected routine identity.
type SizeInquiry struct {
	expr.Info
	Kind SizeKind
	Arg expr.Expr
	Fork expr.Expr
}

func bindRelationSize(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [2]Argument
	var inline [2]string
	names := inline[:0]
	if len(raw.Args) <= 2 {
		names = inline[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil { return nil, err }
		if i < 2 { operands[i] = arg }
		if i < len(names) {
			name := "unknown"
			if arg.Usage.Type != nil { name = b.DiagnosticTypeName(arg.Usage.Type) }
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 { return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos) }
	if err := ValidateCallSyntax(b, raw); err != nil { return nil, err }
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil { return nil, err }
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := SizeKind(len(sizeNames))
	for i, candidate := range sizeNames { if name == candidate { kind = SizeKind(i); break } }
	if !visible || kind >= SizeKind(len(sizeNames)) || len(raw.Args) < 1 || len(raw.Args) > 2 || len(raw.Args) == 2 && kind != RelationSize || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	var formals [2]sqltype.ID
	formals[0], formals[1] = sqltype.Regclass, sqltype.Text
	if kind == DatabaseSize || kind == TablespaceSize {
		// Unknowns prefer the string category. Known types use the ordinary
		// implicit coercion graph; Go parameter values never select a formal.
		formals[0] = sqltype.Name
		if operands[0].Usage.Type != nil && !b.CanCoerce(operands[0].Usage, b.Type(sqltype.Name).Usage(), sqltype.Implicit) {
			formals[0] = sqltype.Oid
		}
	}
	for i := range raw.Args {
		if operands[i].Usage.Type != nil && !b.CanCoerce(operands[i].Usage, b.Type(formals[i]).Usage(), sqltype.Implicit) {
			return nil, b.SignatureError(raw.Name, raw.Pos, names)
		}
	}
	var children [2]expr.Expr
	properties := expr.Properties{Volatility: expr.Volatile}
	for i := range raw.Args {
		target := b.Type(formals[i]).Usage()
		if err := b.CaptureTypeInput(target.Type); err != nil { return nil, err }
		child, err := b.CoerceArgument(operands[i], CoercionRequest{Intent: FinalizeArgument, Target: target})
		if err != nil { return nil, err }
		if child.ResultUsage() != target {
			plan, err := sqltype.PlanCoercion(child.ResultUsage(), target, sqltype.Implicit)
			if err != nil { return nil, err }
			child, err = b.CoerceArgument(Argument{Expression: child, Raw: raw.Args[i], Usage: child.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
			if err != nil { return nil, err }
		}
		children[i] = child
		properties = expr.MergeProperties(properties, child.Properties())
	}
	if err := CallDecorationError(raw); err != nil { return nil, err }
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Int8).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, formals[:len(raw.Args)]...)
	if err != nil { return nil, err }
	b.NoteCatalogSnapshot()
	b.NoteCatalogText()
	return SizeInquiry{Info: info, Kind: kind, Arg: children[0], Fork: children[1]}, nil
}

func (n SizeInquiry) Validate() error {
	if n.Kind >= SizeKind(len(sizeNames)) || n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Int8 {
		return fmt.Errorf("stuntgres: invalid size inquiry signature")
	}
	id := n.Arg.ResultUsage().Type.ID()
	if n.Kind < DatabaseSize && id != sqltype.Regclass || n.Kind >= DatabaseSize && id != sqltype.Name && id != sqltype.Oid {
		return fmt.Errorf("stuntgres: invalid size inquiry operand")
	}
	if n.Fork != nil && (n.Kind != RelationSize || n.Fork.ResultUsage().Type == nil || n.Fork.ResultUsage().Type.ID() != sqltype.Text) {
		return fmt.Errorf("stuntgres: invalid size inquiry fork")
	}
	return nil
}

func (n SizeInquiry) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil { return sqltype.Value{}, err }
	arg, err := n.Arg.Eval(ctx)
	if err != nil { return sqltype.Value{}, err }
	fork := "main"
	null := arg.IsNull()
	if n.Fork != nil {
		value, err := n.Fork.Eval(ctx)
		if err != nil { return sqltype.Value{}, err }
		null = null || value.IsNull()
		if !value.IsNull() {
			fork, err = value.Text()
			if err != nil { return sqltype.Value{}, err }
		}
	}
	if null { return sqltype.Null(n.Usage.Type), nil }
	runtime, err := catalogInquiryRuntime(ctx)
	if err != nil { return sqltype.Value{}, err }
	return runtime.SizeInquiry(n.Kind, arg, fork, n.Usage.Type)
}

func (n SizeInquiry) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.ScalarSource(value), nil
}
