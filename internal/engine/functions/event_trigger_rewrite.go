package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

var eventTriggerRewriteNames = []string{"pg_event_trigger_table_rewrite_oid", "pg_event_trigger_table_rewrite_reason"}

// EventTriggerRewriteRuntime supplies execution-time lexical values, never
// command authority or state retained by a prepared expression.
type EventTriggerRewriteRuntime interface {
	EventTriggerRewriteValue(bool, *sqltype.Descriptor) (sqltype.Value, error)
}

type EventTriggerRewrite struct {
	expr.Info
	OID bool
}

func rewriteHelperName(oid bool) string {
	if oid { return eventTriggerRewriteNames[0] }
	return eventTriggerRewriteNames[1]
}

func EventTriggerRewriteContextError(oid bool) error {
	return &sqlstate.Error{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"39P03", Message:rewriteHelperName(oid)+"() can only be called in a table_rewrite event trigger function"}
}

func bindEventTriggerRewrite(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var names []string
	if len(raw.Args) <= 100 && len(raw.Args) != 0 { names = make([]string, len(raw.Args)) }
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil { return nil, err }
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
	if !visible || len(raw.Args)!=0 || len(raw.ArgNames)!=0 || raw.Variadic || raw.ResolutionOnly { return nil, b.SignatureError(raw.Name,raw.Pos,names) }
	oid := raw.Name.Parts[len(raw.Name.Parts)-1].Text == eventTriggerRewriteNames[0]
	name := rewriteHelperName(oid)
	if raw.Star { return nil, expr.ErrorAt("42809",name+"(*) specified, but "+name+" is not an aggregate function",raw.Pos) }
	if err := CallDecorationError(raw); err != nil { return nil,err }
	typ := sqltype.Int4
	if oid { typ = sqltype.Oid }
	info,err := b.BuiltinRoutineInfo(b.Info(b.Type(typ).Usage(),raw.Pos,expr.Properties{Volatility:expr.Stable}),CatalogRoutineProvider,name)
	if err != nil { return nil,err }
	return EventTriggerRewrite{Info:info,OID:oid},nil
}

func (n EventTriggerRewrite) Validate() error {
	want := sqltype.Int4
	if n.OID { want = sqltype.Oid }
	if n.Usage.Type == nil || n.Usage.Type.ID()!=want { return fmt.Errorf("stuntgres: invalid event rewrite result type") }
	return nil
}
func (n EventTriggerRewrite) Eval(ctx expr.Context) (sqltype.Value,error) {
	if _,err:=evaluationContext(ctx); err!=nil { return sqltype.Value{},err }
	if err:=n.Validate(); err!=nil { return sqltype.Value{},err }
	runtime,ok:=ctx.(EventTriggerRewriteRuntime)
	if !ok { return sqltype.Value{},EventTriggerRewriteContextError(n.OID) }
	return runtime.EventTriggerRewriteValue(n.OID,n.Usage.Type)
}
func (n EventTriggerRewrite) OpenSource(ctx expr.Context) (expr.SourceCursor,error) {
	value,err:=n.Eval(ctx)
	if err!=nil { return expr.SourceCursor{},err }
	return expr.ScalarSource(value),nil
}
func (n EventTriggerRewrite) ScalarSourceOutName() string { if n.OID { return "oid" }; return "" }
func (EventTriggerRewrite) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (EventTriggerRewrite) PlanningFold() expr.Fold { return expr.FoldNever }
func (EventTriggerRewrite) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (EventTriggerRewrite) LooksLikeFunction() bool { return true }
func (EventTriggerRewrite) ClosedDefault(func(expr.Expr)bool) bool { return false }
func (EventTriggerRewrite) SourceAdmission() (bool,bool) { return true,false }
func (EventTriggerRewrite) Children() []expr.Expr { return nil }
func (EventTriggerRewrite) GroupLeaf() {}
func (n EventTriggerRewrite) SameGroup(other expr.Expr,_ func(expr.Expr,expr.Expr)bool,_ bool) bool { o,ok:=other.(EventTriggerRewrite); return ok && n.OID==o.OID }
func (n EventTriggerRewrite) Substitute(info expr.Info,children []expr.Expr) (expr.Expr,error) {
	if len(children)!=0 { return nil,fmt.Errorf("stuntgres: invalid event rewrite children") }
	n.Info=info
	return n,nil
}
func (n EventTriggerRewrite) Rebuild(info expr.Info,children []expr.Expr) (expr.Expr,error) {
	next,err:=n.Substitute(info,children)
	if err!=nil { return nil,err }
	v:=next.(EventTriggerRewrite)
	return v,v.Validate()
}
func (n EventTriggerRewrite) Render() (expr.Rendering,error) {
	if err:=n.Validate(); err!=nil { return expr.Rendering{},err }
	return expr.Rendering{Tree:"EventTriggerRewrite",Form:expr.FormCall,Name:rewriteHelperName(n.OID)},nil
}
func (EventTriggerRewrite) Capture(kind expr.CaptureKind) (expr.CaptureOperands,bool,error) { return expr.CaptureOperands{},kind==expr.CaptureFunction,nil }
func (n EventTriggerRewrite) ValidateStoredProvider() error { return n.Validate() }
