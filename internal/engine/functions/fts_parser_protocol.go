package functions

import "stuntgres/internal/engine/expr"

func (e FTSParser) IntrinsicVolatility() expr.Volatility { if e.OID { return expr.Immutable }; return expr.Stable }
func (FTSParser) PlanningFold() expr.Fold { return expr.FoldNever }
func (FTSParser) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (FTSParser) LooksLikeFunction() bool { return true }
func (FTSParser) ClosedDefault(func(expr.Expr)bool) bool { return false }
func (e FTSParser) ValidateStoredProvider() error { return e.Validate() }
func (FTSParser) ClosureTransparent() {}
func (e FTSParser) SnapshotDemand(func(expr.Expr)bool) (bool,bool,bool) { return true,true,false }
func (e FTSParser) SameGroup(other expr.Expr,same func(expr.Expr,expr.Expr)bool,definition bool) bool {
	o,ok := other.(FTSParser)
	if !ok || e.Parse!=o.Parse || e.OID!=o.OID || e.Order!=o.Order { return false }
	for i,arg := range e.Children() { if !same(arg,o.Args[i]) { return false } }
	return true
}
func (e FTSParser) Render() (expr.Rendering,error) {
	if err := e.Validate(); err!=nil { return expr.Rendering{},err }
	args := renderArgs(e.Children())
	if e.Order[0] != 0 {
		labels := parserLabels(e.name(),e.OID)
		for i := range args { args[i].Label = labels[e.Order[i]] }
	}
	r := writtenCall("text parser "+e.name(),e.Children(),e.name(),args,false)
	r.Namespace="pg_catalog"
	r.Replay.QuoteNamespace=true
	r.Replay.Validate=e.Validate
	return r,nil
}
func (e FTSParser) Capture(kind expr.CaptureKind) (expr.CaptureOperands,bool,error) {
	if kind!=expr.CaptureFunction { return expr.CaptureOperands{},false,nil }
	return expr.CaptureOperands{Count:e.arity(),Indexed:true},true,nil
}
func (e FTSParser) CaptureOperand(written int) (expr.Expr,error) {
	if written >= 0 && written < e.arity() { return e.Args[written],nil }
	return nil,expr.ErrorAt("XX000","invalid text parser capture operand",e.Pos)
}
