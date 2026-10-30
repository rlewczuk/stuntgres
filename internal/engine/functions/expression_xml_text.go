package functions

import (
 "fmt"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

// XMLTextCall is an ordinary strict immutable routine, not a demanded special
// expression. Its accepted recipe retains only routine identity and one child.
type XMLTextCall struct {expr.Info;Child expr.Expr;Comment bool}
func(e XMLTextCall) Eval(c expr.Context)(sqltype.Value,error){
 value,err:=e.Child.Eval(c);if err!=nil{return sqltype.Value{},err};if value.IsNull(){return sqltype.Null(e.Usage.Type),nil};ctx,err:=evaluationContext(c);if err!=nil{return sqltype.Value{},err};text,err:=value.Text();if err!=nil{return sqltype.Value{},err};return sqltype.XMLText(ctx,e.Usage.Type,text,e.Comment)
}
func(e XMLTextCall) Children()[]expr.Expr{return []expr.Expr{e.Child}}
func(e XMLTextCall) ValidateStoredProvider()error{if e.Child==nil||e.Child.ResultUsage().Type==nil||e.Child.ResultUsage().Type.ID()!=sqltype.Text||e.Usage.Type==nil||e.Usage.Type.ID()!=sqltype.XML{return fmt.Errorf("invalid XML text routine recipe")};return nil}
func(e XMLTextCall) Rebuild(info expr.Info,children []expr.Expr)(expr.Expr,error){if err:=e.ValidateStoredProvider();err!=nil{return nil,err};if len(children)!=1{return nil,fmt.Errorf("invalid XML text routine children")};e.Info,e.Child=info,children[0];return e,e.ValidateStoredProvider()}
func(e XMLTextCall) IntrinsicVolatility()expr.Volatility{return expr.Immutable}
func(e XMLTextCall) PlanningFold()expr.Fold{return expr.FoldConstant}
func(e XMLTextCall) PreflightFold()expr.Preflight{return expr.PreflightNullOrEvaluate}
func(e XMLTextCall) StrictNull()expr.Strict{return expr.StrictAfter}
func(e XMLTextCall) LooksLikeFunction()bool{return true}
func(e XMLTextCall) ClosureTransparent(){}
func(e XMLTextCall) Capture(kind expr.CaptureKind)(expr.CaptureOperands,bool,error){return expr.CaptureOperands{Count:1,Inline:[2]expr.Expr{e.Child}},kind==expr.CaptureFunction,nil}
func(e XMLTextCall) Name()string{if e.Comment{return "xmlcomment"};return "xmltext"}
func(e XMLTextCall) Render()(expr.Rendering,error){return expr.Rendering{Tree:e.Name(),Inputs:e.Children(),Form:expr.FormCall,Name:e.Name(),Args:[]expr.RenderArg{{Expr:e.Child}}},e.ValidateStoredProvider()}
func(e XMLTextCall) SameGroup(other expr.Expr,same func(expr.Expr,expr.Expr)bool,definition bool)bool{o,ok:=other.(XMLTextCall);return ok&&e.Comment==o.Comment&&same(e.Child,o.Child)}
func bindXMLTextCall(b Binder,raw parser.FunctionExpr)(expr.Expr,error){
 name:=raw.Name.Parts[len(raw.Name.Parts)-1].Text
 if len(raw.Args)!=1||raw.Variadic||raw.ResolutionOnly{return nil,b.CallSignatureError(raw)}
 operand,err:=b.AnalyzeArgument(raw.Args[0],AnalysisRequest{Intent:ComparisonArgument});if err!=nil{return nil,err}
 visible,err:=b.BuiltinVisible(raw.Name);if err!=nil{return nil,err};if !visible{return nil,b.CallSignatureError(raw)}
 child,err:=coerceInquiryFormal(b,operand,raw.Args[0],b.Type(sqltype.Text).Usage(),func()error{return b.CallSignatureError(raw)});if err!=nil{return nil,err}
 if raw.Star{return nil,expr.ErrorAt("42809",name+"(*) specified, but "+name+" is not an aggregate function",raw.Pos)}
 if err:=CallDecorationError(raw);err!=nil{return nil,err}
 info,err:=b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.XML).Usage(),raw.Pos,child.Properties()),CatalogRoutineProvider,name,sqltype.Text);if err!=nil{return nil,err}
 node:=XMLTextCall{Info:info,Child:child,Comment:name=="xmlcomment"};return node,node.ValidateStoredProvider()
}
