package analyze

import (
 "fmt"
 "slices"
 "strings"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
 "stuntgres/internal/xml"
)

func(b *Binder) xmlMappedChild(raw parser.Expr)(expr.Expr,error){
 operand,err:=b.AnalyzeArgument(raw,functions.AnalysisRequest{Intent:functions.ComparisonArgument});if err!=nil{return nil,err}
 child:=operand.Expression
 if operand.Usage.Type==nil {
  // Retain this occurrence, not a future shared-slot type. Finish owns the
  // unresolved/mismatched diagnostic after all ordinary name/child analysis.
  if b.RetainUntypedParameter(child){return child,nil}
  child,err=b.CoerceArgument(operand,functions.CoercionRequest{Intent:functions.FinalizeArgument,Target:b.Type(sqltype.Text).Usage()});if err!=nil{return nil,err}
 }
 if child.ResultUsage().Type!=nil&&child.ResultUsage().Type.ContainsCatalogIdentifier(){b.NoteCatalogText()}
 return child,nil
}
func(b *Binder) bindXMLConstruct(raw parser.XMLConstructExpr)(expr.Expr,error){
 unresolved:=len(b.Params.AnyParameters)
 var children []expr.Expr;var names []string;name:="";properties:=expr.Properties{}
 appendChild:=func(child expr.Expr){children=append(children,child);properties=expr.MergeProperties(properties,child.Properties())}
 for _,named:=range raw.Named {
  child,err:=b.xmlMappedChild(named.Expr);if err!=nil{return nil,err};appendChild(child)
  written:="";inferred:=named.Name==nil
  if inferred {
   column,ok:=named.Expr.(parser.ColumnExpr)
   if !ok||len(column.Name.Parts)==0 {kind:="element";if raw.Kind==parser.XMLElement{kind="attribute"};return nil,expr.ErrorAt("42601","unnamed XML "+kind+" value must be a column reference",named.Pos)}
   written=column.Name.Parts[len(column.Name.Parts)-1].Text
  }else{written=named.Name.Text}
  mapped,err:=xml.MapName(b.Context(),written,inferred);if err!=nil{return nil,err}
  if raw.Kind==parser.XMLElement{for _,prior:=range names{if prior==mapped{return nil,expr.ErrorAt("42601","XML attribute name \""+mapped+"\" appears more than once",named.Pos)}}}
  names=append(names,mapped)
 }
 // Scalar constructors retain opener-owned assignment spines. Unlike XMLTABLE,
 // they do not publish written-source conversion recipes into stored syntax.
 for _,operand:=range raw.Args {
  var child expr.Expr;var err error
  switch raw.Kind {
  case parser.XMLConcat,parser.XMLRoot:child,err=b.xmlSource(operand,b.Type(sqltype.XML).Usage(),strings.ToUpper(raw.Kind.String()),raw.Pos,nil)
  case parser.XMLPI:child,err=b.xmlSource(operand,b.Type(sqltype.Text).Usage(),"XMLPI",raw.Pos,nil)
  default:child,err=b.xmlMappedChild(operand)
  }
  if err!=nil{return nil,err};appendChild(child)
 }
 if raw.Version!=nil {child,err:=b.xmlSource(raw.Version,b.Type(sqltype.Text).Usage(),"XMLROOT",raw.Pos,nil);if err!=nil{return nil,err};appendChild(child)}
 if raw.Kind==parser.XMLElement||raw.Kind==parser.XMLPI{var err error;name,err=xml.MapName(b.Context(),raw.Name.Text,false);if err!=nil{return nil,err}}
 if raw.Kind==parser.XMLElement||raw.Kind==parser.XMLForest{properties.Volatility=max(properties.Volatility,expr.Stable)}
 if b.Params.NoParameters||b.Consumer.frozenDefinition {
  raw.Args=append([]parser.Expr(nil),raw.Args...);raw.Named=append([]parser.XMLNamedOperand(nil),raw.Named...)
  for i:=range raw.Args{raw.Args[i]=b.NormalizeStoredExpression(raw.Args[i])}
  for i:=range raw.Named{raw.Named[i].Expr=b.NormalizeStoredExpression(raw.Named[i].Expr)}
  if raw.Version!=nil{raw.Version=b.NormalizeStoredExpression(raw.Version)}
 }
 info:=b.Info(b.Type(sqltype.XML).Usage(),raw.Pos,properties)
 if len(b.Params.AnyParameters)>unresolved {
  return xmlUnresolvedConstruct{Info:info,children:children},nil
 }
 return functions.NewXMLConstruct(b.Context(),info,raw,name,names,children)
}

// xmlUnresolvedConstruct exists only between ordinary child analysis and Finish.
// Every instance retains an UNKNOWN occurrence in AnyParameters, so Finish must
// reject before successful Describe publication. It deliberately has no provider,
// capture or execution authority; the genuine constructor factory stays strict.
type xmlUnresolvedConstruct struct {
 expr.Info
 children []expr.Expr
}
func(e xmlUnresolvedConstruct) Children()[]expr.Expr{return slices.Clone(e.children)}
func(e xmlUnresolvedConstruct) Rebuild(info expr.Info,children []expr.Expr)(expr.Expr,error){
 if info.Usage!=e.Usage||len(children)!=len(e.children){return nil,fmt.Errorf("invalid unresolved XML analysis rebuild")}
 for i,child:=range children{if child==nil||child.ResultUsage()!=e.children[i].ResultUsage(){return nil,fmt.Errorf("invalid unresolved XML analysis child")}}
 e.Info,e.children=info,slices.Clone(children);return e,nil
}
func(e xmlUnresolvedConstruct) Eval(expr.Context)(sqltype.Value,error){
 return sqltype.Value{},fmt.Errorf("unresolved XML analysis expression reached execution")
}
