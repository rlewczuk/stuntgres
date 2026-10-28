package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

var runtimeInfoNames = [...]string{"pg_jit_available", "pg_numa_available", "pg_get_loaded_modules", "pg_current_logfile"}

// RuntimeCapability is an immutable inquiry recipe, not physical runtime state.
type RuntimeCapability struct {
	expr.Info
	NUMA bool
}

// LoadedModules owns the fixed output shape, never a fabricated library registry.
type LoadedModules struct { expr.Info }

// CurrentLogfile retains only its optional typed format operand.
type CurrentLogfile struct {
	expr.Info
	Format expr.Expr
}

func bindRuntimeInfo(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
		if err != nil { return nil, err }
		if len(raw.Args) == 1 { operand = arg }
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
	index := -1
	for i, candidate := range runtimeInfoNames { if candidate == name { index = i; break } }
	if !visible || index < 0 || len(raw.Args) > 1 || index != 3 && len(raw.Args) != 0 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	var arg expr.Expr
	if len(raw.Args) == 1 {
		target := b.Type(sqltype.Text).Usage()
		arg, err = b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: target})
		if err != nil { return nil, err }
		if arg.ResultUsage() != target {
			plan, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
			if failure != nil {
				if diagnostic, ok := failure.(*sqlstate.Error); ok && (diagnostic.Code == "42804" || diagnostic.Code == "42846") { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
				return nil, failure
			}
			arg, err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[0], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
			if err != nil { return nil, err }
		}
	}
	if raw.Star { return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos) }
	if err := CallDecorationError(raw); err != nil { return nil, err }
	usage := b.Type(sqltype.Bool).Usage()
	volatility := expr.Volatile
	if index == 1 { volatility = expr.Stable }
	if index == 2 {
		if err := b.AdmitSetReturning(raw.Pos); err != nil { return nil, err }
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{
			{Attribute: 1, Name: "module_name", Usage: b.Type(sqltype.Text).Usage()},
			{Attribute: 2, Name: "version", Usage: b.Type(sqltype.Text).Usage()},
			{Attribute: 3, Name: "file_name", Usage: b.Type(sqltype.Text).Usage()},
		})
		if err != nil { return nil, err }
		usage = shape.Usage()
	} else if index == 3 { usage = b.Type(sqltype.Text).Usage() }
	properties := expr.Properties{Volatility: volatility}
	if arg != nil { properties = arg.Properties(); properties.Volatility = max(volatility, properties.Volatility) }
	var info expr.Info
	if arg != nil { info, err = b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, properties), CatalogRoutineProvider, name, sqltype.Text) } else { info, err = b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, properties), CatalogRoutineProvider, name) }
	if err != nil { return nil, err }
	switch index {
	case 2: return LoadedModules{info}, nil
	case 3: return CurrentLogfile{info, arg}, nil
	default: return RuntimeCapability{info, index == 1}, nil
	}
}

func (n RuntimeCapability) Validate() error {
	if n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Bool { return fmt.Errorf("stuntgres: invalid runtime capability signature") }
	return nil
}
func (n LoadedModules) Validate() error {
	d := n.Usage.Type
	if d == nil || d.ID() != 2249 || !d.IsComposite() || d.CompositeLen() != 3 { return fmt.Errorf("stuntgres: invalid loaded modules record") }
	for i, name := range [...]string{"module_name", "version", "file_name"} {
		m := d.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != name || m.Usage.Type == nil || m.Usage.Type.ID() != sqltype.Text { return fmt.Errorf("stuntgres: invalid loaded modules member") }
	}
	return nil
}
func (n CurrentLogfile) Validate() error {
	if n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Text || n.Format != nil && (n.Format.ResultUsage().Type == nil || n.Format.ResultUsage().Type.ID() != sqltype.Text) { return fmt.Errorf("stuntgres: invalid current logfile signature") }
	return nil
}
func (n RuntimeCapability) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil { return sqltype.Value{}, err }
	if err := n.Validate(); err != nil { return sqltype.Value{}, err }
	return sqltype.Boolean(n.Usage.Type, false), nil
}
func (LoadedModules) Eval(expr.Context) (sqltype.Value, error) { return sqltype.Value{}, fmt.Errorf("stuntgres: loaded modules set used as scalar") }
func (n LoadedModules) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if _, err := evaluationContext(ctx); err != nil { return expr.SourceCursor{}, err }
	if err := n.Validate(); err != nil { return expr.SourceCursor{}, err }
	return expr.OwnedSource(n.Usage.Type, nil), nil
}
func (n CurrentLogfile) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil { return sqltype.Value{}, err }
	if err := n.Validate(); err != nil { return sqltype.Value{}, err }
	if n.Format != nil {
		value, err := n.Format.Eval(ctx)
		if err != nil { return sqltype.Value{}, err }
		if !value.IsNull() {
			format, err := value.Text()
			if err != nil { return sqltype.Value{}, err }
			if format != "stderr" && format != "csvlog" && format != "jsonlog" {
				return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "log format \""+format+"\" is not supported", Hint: "The supported log formats are \"stderr\", \"csvlog\", and \"jsonlog\"."}
			}
		}
	}
	return sqltype.Null(n.Usage.Type), nil
}
func (n RuntimeCapability) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.ScalarSource(value), nil
}
func (n CurrentLogfile) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.ScalarSource(value), nil
}
