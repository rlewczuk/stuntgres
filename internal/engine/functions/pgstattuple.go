package functions

import (
 "fmt"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

// HeapStatisticsKind selects a released pgstattuple kernel, never a public name.
type HeapStatisticsKind uint8
const (
 HeapStatisticsExact HeapStatisticsKind = iota
 HeapStatisticsApprox
 HeapStatisticsPages
)

type Pgstattuple struct {
 expr.Info
 Args []expr.Expr
 Input sqltype.ID
 Kind HeapStatisticsKind
}

func bindPgstattuple(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
 operands := make([]Argument, len(raw.Args))
 for i, child := range raw.Args {
  v, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
  if err != nil { return nil, err }; operands[i] = v
 }
 if err := ValidateCallSyntax(b, raw); err != nil { return nil, err }
 routine, err := b.InstalledRoutine(raw.Name, PgstattupleExtension, "")
 if err != nil { return nil, err }
 if routine == nil || len(operands) != 1 || raw.ResolutionOnly || raw.Variadic { return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands)) }
 if err := routine.Validate(); err != nil { return nil, err }
 if err := CallDecorationError(raw); err != nil { return nil, err }
 authority, ok := routine.(interface{ PgstattupleDeclaration() (HeapStatisticsKind, sqltype.ID, string, []sqltype.CompositeMember) })
 if !ok { return nil, fmt.Errorf("stuntgres: missing pgstattuple declaration") }
 kind, input, formal, members := authority.PgstattupleDeclaration()
 if _, ok := namedSlots(raw, []string{formal}, 1); !ok { return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands)) }
 if raw.Star { return nil, expr.ErrorAt("42809", raw.Name.Parts[len(raw.Name.Parts)-1].Text+"(*) specified, but "+raw.Name.Parts[len(raw.Name.Parts)-1].Text+" is not an aggregate function", raw.Pos) }
 target := b.Type(input).Usage()
 if operands[0].Usage.Type != nil {
  if _, err := sqltype.PlanCoercion(operands[0].Usage, target, sqltype.Implicit); err != nil { return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands)) }
 }
 child, err := b.CoerceArgument(operands[0], CoercionRequest{Intent: DirectFormalArgument, Target: target})
 if err != nil { return nil, err }
 usage := b.Type(sqltype.Int8).Usage()
 if kind != HeapStatisticsPages {
  shape, err := sqltype.RecordShape(members); if err != nil { return nil, err }; usage = shape.Usage()
 }
 n := Pgstattuple{Args: []expr.Expr{child}, Input: input, Kind: kind}
 n.Info = b.Info(usage, raw.Pos, expr.MergeProperties(expr.Properties{Volatility: expr.Volatile, StageDependent: true}, child.Properties()))
 return routine.Attach(n)
}

func namedPgstattuple(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
 // The shared installed-call selector passes the authentic canonical symbol;
 // public renames never select a different formal declaration.
 formal := "relname"
 if len(declared)==1 && declared[0].Type != nil && declared[0].Type.ID()==sqltype.Regclass && raw.Name.Parts[len(raw.Name.Parts)-1].Text != "pg_relpages" { formal="reloid" }
 slots, ok := namedSlots(raw, []string{formal}, 1)
 if !ok || raw.Variadic || len(args)!=1 || len(declared)!=1 { return nil,nil,false }
 return namedInputs(b,args,declared,slots[:1])
}
func (Pgstattuple) Eval(expr.Context) (sqltype.Value,error) { return sqltype.Value{},fmt.Errorf("stuntgres: pgstattuple requires installed invocation authority") }
func (n Pgstattuple) Children() []expr.Expr { return n.Args }
func (n Pgstattuple) ValidateArguments(children []expr.Expr) error {
 if n.Kind>HeapStatisticsPages || n.Input!=sqltype.Text && n.Input!=sqltype.Regclass || n.Kind==HeapStatisticsApprox && n.Input!=sqltype.Regclass || len(children)!=1 || children[0]==nil || children[0].ResultUsage().Type==nil || children[0].ResultUsage().Type.BaseType().ID()!=n.Input { return fmt.Errorf("stuntgres: invalid pgstattuple argument") }; return nil
}
func (n Pgstattuple) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr,error) {
 if err:=n.ValidateArguments(children); err!=nil { return nil,err }; n.Info=info; n.Args=append([]expr.Expr(nil),children...); return n,nil
}
func (n Pgstattuple) Substitute(info expr.Info, children []expr.Expr) (expr.Expr,error) { return n.Rebuild(info,children) }
func (Pgstattuple) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (Pgstattuple) PlanningFold() expr.Fold { return expr.FoldNever }
func (Pgstattuple) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (Pgstattuple) LooksLikeFunction() bool { return true }
func (n Pgstattuple) Capture(kind expr.CaptureKind) (expr.CaptureOperands,bool,error) { return expr.CaptureOperands{Args:n.Args,Count:1},kind==expr.CaptureFunction,nil }
func (Pgstattuple) SourceAdmission() (bool,bool) { return true,false }

func (n Pgstattuple) StrictNull() expr.Strict {
	if n.Kind == HeapStatisticsPages {
		return expr.StrictAfterBare
	}
	return expr.StrictNone
}
