package functions

import (
 "fmt"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

// DatabaseFilesKind selects a released location inquiry, never a physical mapping.
type DatabaseFilesKind uint8
const (
 RelationFilenode DatabaseFilesKind = iota
 RelationFilepath
 FilenodeRelation
)
var databaseFilesNames = [...]string{"pg_relation_filenode", "pg_relation_filepath", "pg_filenode_relation"}
func databaseFilesArity(kind DatabaseFilesKind) int { if kind == FilenodeRelation { return 2 }; return 1 }
func databaseFilesResult(kind DatabaseFilesKind) sqltype.ID {
 switch kind { case RelationFilepath: return sqltype.Text; case FilenodeRelation: return sqltype.Regclass; default: return sqltype.Oid }
}
// DatabaseFilesInquiry owns typed operands and the selected routine identity only.
type DatabaseFilesInquiry struct {
 expr.Info
 Kind DatabaseFilesKind
 Arg expr.Expr
 Second expr.Expr
}

func bindDatabaseFiles(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	kind := DatabaseFilesKind(len(databaseFilesNames))
	for i, candidate := range databaseFilesNames { if name == candidate { kind = DatabaseFilesKind(i); break } }
	if !visible || kind >= DatabaseFilesKind(len(databaseFilesNames)) || len(raw.Args) != databaseFilesArity(kind) || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	var formals [2]sqltype.ID
	formals[0], formals[1] = sqltype.Regclass, sqltype.Oid
	if kind == FilenodeRelation { formals[0] = sqltype.Oid }
	for i := range raw.Args {
		if operands[i].Usage.Type != nil && !b.CanCoerce(operands[i].Usage, b.Type(formals[i]).Usage(), sqltype.Implicit) {
			return nil, b.SignatureError(raw.Name, raw.Pos, names)
		}
	}
	var children [2]expr.Expr
	properties := expr.Properties{Volatility: expr.Stable}
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
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(databaseFilesResult(kind)).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, formals[:len(raw.Args)]...)
	if err != nil { return nil, err }
	b.NoteCatalogSnapshot()
	b.NoteCatalogText()
	return DatabaseFilesInquiry{Info: info, Kind: kind, Arg: children[0], Second: children[1]}, nil
}

func (n DatabaseFilesInquiry) Validate() error {
 if n.Kind >= DatabaseFilesKind(len(databaseFilesNames)) || n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Usage.Type == nil || n.Usage.Type.ID() != databaseFilesResult(n.Kind) {
  return fmt.Errorf("stuntgres: invalid database files inquiry signature")
 }
 formal := sqltype.Regclass
 if n.Kind == FilenodeRelation { formal = sqltype.Oid }
 if n.Arg.ResultUsage().Type.ID() != formal || (n.Second != nil) != (n.Kind == FilenodeRelation) { return fmt.Errorf("stuntgres: invalid database files inquiry operand") }
 if n.Second != nil && (n.Second.ResultUsage().Type == nil || n.Second.ResultUsage().Type.ID() != sqltype.Oid) { return fmt.Errorf("stuntgres: invalid database files inquiry second operand") }
 return nil
}
func (n DatabaseFilesInquiry) Eval(ctx expr.Context) (sqltype.Value, error) {
 if _, err := evaluationContext(ctx); err != nil { return sqltype.Value{}, err }
 arg, err := n.Arg.Eval(ctx)
 if err != nil { return sqltype.Value{}, err }
 var second sqltype.Value
 null := arg.IsNull()
 if n.Second != nil {
  second, err = n.Second.Eval(ctx)
  if err != nil { return sqltype.Value{}, err }
  null = null || second.IsNull()
 }
 if null { return sqltype.Null(n.Usage.Type), nil }
 runtime, err := catalogInquiryRuntime(ctx)
 if err != nil { return sqltype.Value{}, err }
 return runtime.DatabaseFilesInquiry(n.Kind, arg, second, n.Usage.Type)
}
func (n DatabaseFilesInquiry) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
 value, err := n.Eval(ctx)
 if err != nil { return expr.SourceCursor{}, err }
 return expr.ScalarSource(value), nil
}
