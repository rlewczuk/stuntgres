package analyze

import (
 "fmt"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

func (b *Binder) bindXMLTable(raw *parser.XMLTable, ordinal int) (plan.Node,QueryNamespace,error) {
 oldClause:=b.Clause.AggregateClause
 b.Clause.AggregateClause="FROM"
 defer func(){b.Clause.AggregateClause=oldClause}()
 oldForbidden:=b.Clause.stringFromForbidden
 b.Clause.stringFromForbidden=false
 defer func(){b.Clause.stringFromForbidden=oldForbidden}()
 if b.Query.Namespace==nil { b.Query.Namespace=&QueryNamespace{};defer func(){b.Query.Namespace=nil}() }
 bound,err:=b.xmlTableExpression(raw)
 if err!=nil { return nil,QueryNamespace{},err }
 shape:=bound.ResultUsage().Type
 schema:=make([]catalog.Column,shape.CompositeLen())
 columns:=make([]QueryColumn,len(schema))
 for i:=range schema {
  member:=shape.CompositeMember(i)
  schema[i]=catalog.Column{Name:member.Name,Usage:member.Usage}
  columns[i]=QueryColumn{Descriptor:schema[i],Slot:i,Leaf:-1,Physical:-1}
 }
 q:=QueryQualifier{Name:"xmltable",Original:"xmltable",Columns:columns}
 if ordinal>=0 {
  for i,child:=range raw.Expressions() {
   path:=ViewLiteralPath{Query:b.FrameID(),Clause:ViewFunctionLiteral,Source:ordinal,Target:i}
   at:=0
   if err:=b.CaptureViewLiteralTree(child,bound.Args[i],path,&at);err!=nil { return nil,QueryNamespace{},err }
  }
  // Source capture can install builtin provider markers. Retain the final
  // normalized witness only after every capture, before publishing this value.
  bound.Raw=b.normalizeInListFrom(raw).(*parser.XMLTable)
 }
 captures:=b.functionCaptures([]expr.Expr{bound})
 b.Query.Hidden=append(b.Query.Hidden,q)
 node:=&plan.FunctionFromNode{QuerySchema:schema,Calls:[]expr.Expr{bound},Frame:b.FrameID(),Correlated:len(captures)>0,Captures:captures,RegexpNamespace:true,ViewOrdinal:ordinal}
 return node,QueryNamespace{Columns:columns,Qualifiers:[]QueryQualifier{q},Frame:schema},nil
}
func (b *Binder) xmlTableExpression(raw *parser.XMLTable) (functions.XMLTable,error) {
 fail:=func(err error)(functions.XMLTable,error){return functions.XMLTable{},err}
 names:=make(map[string]bool)
 for _,ns:=range raw.Namespaces {
  if ns.Default { continue }
  if names[ns.Name.Text] { return fail(expr.ErrorAt("42601",fmt.Sprintf("namespace name %q is not unique",ns.Name.Text),ns.Pos)) }
  names[ns.Name.Text]=true
 }
 names=make(map[string]bool)
 ordinal:=false
 for _,column:=range raw.Columns {
  if names[column.Name.Text] { return fail(expr.ErrorAt("42601",fmt.Sprintf("column name %q is not unique",column.Name.Text),column.Name.Pos)) }
  names[column.Name.Text]=true
  if column.Ordinality { if ordinal {return fail(expr.ErrorAt("42601","only one FOR ORDINALITY column is allowed",column.Name.Pos))};ordinal=true }
  if column.Type.Setof { return fail(expr.ErrorAt("42P16",fmt.Sprintf("column %q cannot be declared SETOF",column.Name.Text),column.Name.Pos)) }
  seen:=make(map[string]bool)
  for _,option:=range column.Options {
   kind:=option.Kind
   if kind=="not null" { kind="null" }
   if seen[kind] {
    message:="only one DEFAULT value is allowed"
    if kind=="path" {message="only one PATH value per column is allowed"}
    if kind=="null" {message=fmt.Sprintf("conflicting or redundant NULL / NOT NULL declarations for column %q",column.Name.Text)}
    return fail(expr.ErrorAt("42601",message,option.Pos))
   }
   seen[kind]=true
  }
 }
 written:=raw.Expressions()
 args:=make([]expr.Expr,len(written))
 nsCount:=len(raw.Namespaces)
 for i:=range nsCount+2 {
  target:=b.Type(sqltype.Text).Usage()
  if i==nsCount+1 { target=b.Type(sqltype.XML).Usage() }
  child,err:=b.xmlSource(written[i],target,"XMLTABLE",raw.Pos,written[i])
  if err!=nil { return fail(err) }
  args[i]=child
 }
 fields:=make([]functions.XMLTableField,len(raw.Columns))
 members:=make([]sqltype.CompositeMember,len(fields))
 next:=nsCount+2
 for i,column:=range raw.Columns {
  usage:=b.Type(sqltype.Int4).Usage()
  var err error
  if !column.Ordinality {
   usage,err=b.JSONReturningType(column.Type)
   if err!=nil {return fail(err)}
   if err:=b.CaptureTypeInput(usage.Type);err!=nil {return fail(err)}
  }
  field:=functions.XMLTableField{Usage:usage,Path:-1,Default:-1,Ordinality:column.Ordinality}
  for _,option:=range column.Options {
   switch option.Kind {
   case "not null":field.NotNull=true
   case "path":
    child,err:=b.xmlSource(option.Value,b.Type(sqltype.Text).Usage(),"XMLTABLE",raw.Pos,option.Value)
    if err!=nil {return fail(err)}
    args[next]=child;field.Path=next;next++
   case "default":
    operand,err:=b.AnalyzeArgument(option.Value,functions.AnalysisRequest{Intent:functions.ComparisonArgument})
    if err!=nil {return fail(err)}
    child:=operand.Expression
    if operand.Usage.Type==nil {
     child,err=b.CoerceArgument(operand,functions.CoercionRequest{Intent:functions.FinalizeArgument,Target:usage.BaseUsage()})
     if err!=nil {return fail(err)}
    }
    child,err=b.CoerceSelected(child,usage,sqltype.Assignment,parser.ExpressionStart(option.Value),option.Value)
    if err!=nil {return fail(err)}
    args[next]=child;field.Default=next;next++
   }
  }
  fields[i]=field
  members[i]=sqltype.CompositeMember{Attribute:int32(i+1),Name:column.Name.Text,Usage:usage}
 }
 shape,err:=sqltype.RecordShape(members)
 if err!=nil {return fail(err)}
 properties:=expr.Properties{Volatility:expr.Stable}
 for _,arg:=range args {properties=expr.MergeProperties(properties,arg.Properties())}
 return functions.NewXMLTable(b.Info(shape.Usage(),raw.Pos,properties),raw,args,fields)
}
