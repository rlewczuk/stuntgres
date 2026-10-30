package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type XMLMappingOperation uint8
const (
	XMLTableRows XMLMappingOperation=iota
	XMLQueryRows
	XMLCursorRows
	XMLTableSchema
	XMLQuerySchema
	XMLCursorSchema
	XMLTableCombined
	XMLQueryCombined
)
var xmlMappingNames=[...]string{"table_to_xml","query_to_xml","cursor_to_xml","table_to_xmlschema","query_to_xmlschema","cursor_to_xmlschema","table_to_xml_and_xmlschema","query_to_xml_and_xmlschema"}
func (o XMLMappingOperation) Name()string{if int(o)>=len(xmlMappingNames){return ""};return xmlMappingNames[o]}
func (o XMLMappingOperation) Relation()bool{return o==XMLTableRows||o==XMLTableSchema||o==XMLTableCombined}
func (o XMLMappingOperation) Cursor()bool{return o==XMLCursorRows||o==XMLCursorSchema}
func (o XMLMappingOperation) Schema()bool{return o>=XMLTableSchema}
func (o XMLMappingOperation) Combined()bool{return o==XMLTableCombined||o==XMLQueryCombined}
func (o XMLMappingOperation) Arity()int{if o.Name()==""{return 0};if o==XMLCursorRows{return 5};return 4}
func (o XMLMappingOperation) formal(i int)sqltype.ID{if i==0{if o.Relation(){return sqltype.Regclass};if o.Cursor(){return sqltype.Refcursor};return sqltype.Text};if o==XMLCursorRows&&i==1{return sqltype.Int4};if i==o.Arity()-1{return sqltype.Text};return sqltype.Bool}
func (o XMLMappingOperation) labels()[]string{if o==XMLCursorRows{return []string{"cursor","count","nulls","tableforest","targetns"}};first:="query";if o.Relation(){first="tbl"};if o.Cursor(){first="cursor"};return []string{first,"nulls","tableforest","targetns"}}

type XMLMappingRuntime interface { MapXMLRows(XMLMappingOperation,[5]sqltype.Value,*sqltype.Descriptor,sqltype.OutputEnvironment)(sqltype.Value,error) }

type XMLMapping struct {expr.Info; Operation XMLMappingOperation; Args [5]expr.Expr; Order [5]int}
func (e XMLMapping) Children()[]expr.Expr{return e.Args[:e.Operation.Arity()]}
func (e XMLMapping) Eval(c expr.Context)(sqltype.Value,error){
	if _,err:=evaluationContext(c);err!=nil{return sqltype.Value{},err}
	args:=e.Args[:e.Operation.Arity()]
	for _,arg:=range args{if c.FunctionLiteralNull(arg){return sqltype.Null(e.Usage.Type),nil}}
	var values [5]sqltype.Value
	for i,arg:=range args{value,err:=arg.Eval(c);if err!=nil{return sqltype.Value{},err};values[e.Order[i]]=value}
	for i:=range e.Operation.Arity(){if values[i].IsNull(){return sqltype.Null(e.Usage.Type),nil}}
	runtime,ok:=c.(XMLMappingRuntime);if !ok{return sqltype.Value{},fmt.Errorf("stuntgres: XML row mapping lacks statement authority")}
	return runtime.MapXMLRows(e.Operation,values,e.Usage.Type,c.FunctionXMLOutput())
}
func (e XMLMapping) Validate()error{
	if e.Operation.Name()==""||e.Usage.Type==nil||e.Usage.Type.ID()!=sqltype.XML||!e.Usage.Type.Concrete(){return fmt.Errorf("invalid XML mapping provider")}
	var seen [5]bool
	for i,arg:=range e.Args{if i>=e.Operation.Arity(){if arg!=nil||e.Order[i]!=0{return fmt.Errorf("extra XML mapping child")};continue};slot:=e.Order[i];if arg==nil||slot<0||slot>=e.Operation.Arity()||seen[slot]||arg.ResultUsage().Type==nil||arg.ResultUsage().Type.ID()!=e.Operation.formal(slot){return fmt.Errorf("invalid XML mapping operand")};seen[slot]=true}
	return nil
}
func (e XMLMapping) ValidateStoredProvider()error{return e.Validate()}
func (e XMLMapping) IntrinsicVolatility()expr.Volatility{if e.Operation.Relation(){return expr.Stable};return expr.Volatile}
func (XMLMapping) PreflightFold()expr.Preflight{return expr.PreflightChildren}
func (XMLMapping) PlanningFold()expr.Fold{return expr.FoldNever}
func (XMLMapping) LooksLikeFunction()bool{return true}
func (XMLMapping) ClosureTransparent(){}
func (e XMLMapping) Rebuild(info expr.Info,children []expr.Expr)(expr.Expr,error){if err:=checkChildren("XMLMapping",children,e.Operation.Arity());err!=nil{return nil,err};e.Info=info;copy(e.Args[:],children);return e,e.Validate()}
func (e XMLMapping) Substitute(info expr.Info,children []expr.Expr)(expr.Expr,error){return e.Rebuild(info,children)}
func (e XMLMapping) SameGroup(other expr.Expr,same func(expr.Expr,expr.Expr)bool,_ bool)bool{o,ok:=other.(XMLMapping);if !ok||e.Operation!=o.Operation||e.Order!=o.Order{return false};for i,arg:=range e.Children(){if !same(arg,o.Args[i]){return false}};return true}
func (e XMLMapping) Render()(expr.Rendering,error){
	if err:=e.Validate();err!=nil{return expr.Rendering{},err}
	children:=e.Children();args:=renderArgs(children)
	for i:=range args{if e.Order[i]!=i{labels:=e.Operation.labels();for j:=range args{args[j].Label=labels[e.Order[j]]};break}}
	r:=writtenCall("XML row mapping",children,e.Operation.Name(),args,false)
	r.Namespace="pg_catalog";r.Replay.QuoteNamespace=true;r.Replay.Validate=e.Validate
	return r,nil
}
func (e XMLMapping) Capture(kind expr.CaptureKind)(expr.CaptureOperands,bool,error){if kind!=expr.CaptureFunction{return expr.CaptureOperands{},false,nil};return expr.CaptureOperands{Count:e.Operation.Arity(),Indexed:true},true,nil}
func (e XMLMapping) CaptureOperand(written int)(expr.Expr,error){if written<0||written>=e.Operation.Arity(){return nil,fmt.Errorf("invalid XML mapping capture")};return e.Args[written],nil}
