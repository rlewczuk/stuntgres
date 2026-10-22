package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type TablespaceLocation struct {
	expr.Info
	Arg expr.Expr
}

type TablespaceDatabases struct {
	expr.Info
	Arg expr.Expr
}

func bindTablespaceInfo(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
			if typ := arg.Usage.Type; typ != nil {
				name = b.DiagnosticTypeName(typ)
			}
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	scalar := name == "pg_tablespace_location"
	if !visible || !scalar && name != "pg_tablespace_databases" || len(raw.Args) != 1 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	target := b.Type(sqltype.Oid).Usage()
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
	result := sqltype.Oid
	if scalar {
		result = sqltype.Text
	} else if err := b.AdmitSetReturning(raw.Pos); err != nil {
		return nil, err
	}
	properties := arg.Properties()
	properties.Volatility = max(expr.Stable, properties.Volatility)
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(result).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, sqltype.Oid)
	if err != nil {
		return nil, err
	}
	b.NoteCatalogSnapshot()
	b.NoteCatalogText()
	if scalar {
		return TablespaceLocation{info, arg}, nil
	}
	return TablespaceDatabases{info, arg}, nil
}

func (n TablespaceLocation) Validate() error {
	if n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage().Type.ID() != sqltype.Oid || n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Text {
		return fmt.Errorf("stuntgres: invalid tablespace location signature")
	}
	return nil
}

func (n TablespaceDatabases) Validate() error {
	if n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage().Type.ID() != sqltype.Oid || n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Oid {
		return fmt.Errorf("stuntgres: invalid tablespace databases signature")
	}
	return nil
}

func (n TablespaceLocation) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	value, err := n.Arg.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	oid, err := value.OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, err := catalogInquiryRuntime(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.TablespaceLocation(oid, n.Usage.Type)
}

func (TablespaceDatabases) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: tablespace database set used as scalar")
}

func (n TablespaceDatabases) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return expr.SourceCursor{}, err
	}
	value, err := n.Arg.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if value.IsNull() {
		return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, Exhausted: true}), nil
	}
	oid, err := value.OID()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	runtime, err := catalogInquiryRuntime(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	value, present, err := runtime.TablespaceDatabase(oid, n.Usage.Type)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if !present {
		return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, Exhausted: true}), nil
	}
	return expr.ScalarSource(value), nil
}

func (n TablespaceLocation) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
