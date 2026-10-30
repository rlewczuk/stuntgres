package functions

import (
 "fmt"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

// XMLTableField stores only accepted usages and written child indexes.
type XMLTableField struct { Usage sqltype.Usage; Path, Default int; Ordinality, NotNull bool }
// XMLTable retains no invocation graph or mutable evaluation state.
type XMLTable struct {
 expr.Info
 Raw *parser.XMLTable
 Args []expr.Expr
 Fields []XMLTableField
}
func NewXMLTable(info expr.Info, raw *parser.XMLTable, args []expr.Expr, fields []XMLTableField) (XMLTable,error) {
 e:=XMLTable{Info:info,Raw:raw,Args:args,Fields:fields}
 return e,e.ValidateStoredProvider()
}
func (e XMLTable) ValidateStoredProvider() error {
 if e.Raw==nil || e.Usage.Type==nil || !e.Usage.Type.IsComposite() || len(e.Args)!=len(e.Raw.Expressions()) || len(e.Fields)!=e.Usage.Type.CompositeLen() || len(e.Fields)!=len(e.Raw.Columns) { return fmt.Errorf("invalid XMLTABLE description") }
 for i,arg:=range e.Args {
  if arg==nil || !arg.ResultUsage().Valid() { return fmt.Errorf("invalid XMLTABLE child %d",i) }
 }
 for i:=range len(e.Raw.Namespaces)+1 {
  if e.Args[i].ResultUsage().Type.ID()!=sqltype.Text { return fmt.Errorf("invalid XMLTABLE text child") }
 }
 if e.Args[len(e.Raw.Namespaces)+1].ResultUsage().Type.BaseType().ID()!=sqltype.XML { return fmt.Errorf("invalid XMLTABLE document child") }
 for i,field:=range e.Fields {
  if field.Usage!=e.Usage.Type.CompositeMember(i).Usage || field.Path < -1 || field.Path>=len(e.Args) || field.Default < -1 || field.Default>=len(e.Args) { return fmt.Errorf("invalid XMLTABLE field") }
  if field.Path>=0 && e.Args[field.Path].ResultUsage().Type.ID()!=sqltype.Text { return fmt.Errorf("invalid XMLTABLE filter") }
  if field.Default>=0 && e.Args[field.Default].ResultUsage()!=field.Usage { return fmt.Errorf("foreign XMLTABLE default usage") }
  if field.Ordinality && (field.Usage.Type.ID()!=sqltype.Int4 || field.Path!=-1 || field.Default!=-1) { return fmt.Errorf("invalid XMLTABLE ordinal") }
 }
 return nil
}
func (e XMLTable) Eval(expr.Context) (sqltype.Value,error) { return sqltype.Value{},fmt.Errorf("XMLTABLE requires a relational source") }
func (e XMLTable) SourceAdmission() (bool,bool) { return true,true }
func (e XMLTable) SameSourceOccurrence(other expr.Expr) bool { o,ok:=other.(XMLTable);return ok && e.Usage==o.Usage }
func (e XMLTable) Children() []expr.Expr { return e.Args }
func (e XMLTable) WrittenChildren() []expr.Expr { return e.Args }
func (e XMLTable) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr,error) {
 fields:=e.Fields
 if info.Usage.Type!=e.Usage.Type {
  if info.Usage.Type==nil || !info.Usage.Type.IsComposite() || info.Usage.Type.CompositeLen()!=len(fields) { return nil,fmt.Errorf("invalid XMLTABLE rebuilt shape") }
  fields=append([]XMLTableField(nil),fields...)
  for i:=range fields { fields[i].Usage=info.Usage.Type.CompositeMember(i).Usage }
 }
 return NewXMLTable(info,e.Raw,children,fields)
}
func (e XMLTable) Substitute(info expr.Info, children []expr.Expr) (expr.Expr,error) { return e.Rebuild(info,children) }
func (e XMLTable) IntrinsicVolatility() expr.Volatility { return e.Property.Volatility }
func (e XMLTable) PlanningFold() expr.Fold { return expr.FoldNever }
func (e XMLTable) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (e XMLTable) ClosureTransparent() {}
func (e XMLTable) ResultNullability(expr.ResultSite) (bool,bool) { return true,true }
func (e XMLTable) Render() (expr.Rendering,error) { return expr.Rendering{Tree:"XMLTable",Inputs:e.Args},nil }
func (e XMLTable) Capture(kind expr.CaptureKind) (expr.CaptureOperands,bool,error) { return expr.CaptureOperands{Count:len(e.Args),Indexed:true,XMLSources:true},kind==expr.CaptureSQLJSON,nil }
func (e XMLTable) CaptureOperand(index int) (expr.Expr,error) { if index<0 || index>=len(e.Args) { return nil,fmt.Errorf("invalid XMLTABLE occurrence") };return e.Args[index],nil }
func (e XMLTable) SameGroup(other expr.Expr, same func(expr.Expr,expr.Expr)bool, definition bool) bool {
 o,ok:=other.(XMLTable)
 if !ok || len(e.Fields)!=len(o.Fields) || len(e.Args)!=len(o.Args) || len(e.Raw.Namespaces)!=len(o.Raw.Namespaces) { return false }
 for i:=range e.Fields {
  a,b:=e.Usage.Type.CompositeMember(i),o.Usage.Type.CompositeMember(i)
  if a.Name!=b.Name || a.Usage!=b.Usage { return false }
 }
 for i,ns:=range e.Raw.Namespaces { if ns.Name.Text!=o.Raw.Namespaces[i].Name.Text { return false } }
 for i,field:=range e.Fields { if field!=o.Fields[i] { return false } }
 for i,arg:=range e.Args { if !same(arg,o.Args[i]) { return false } }
 return true
}
func xmlTableError(message, detail string) error { return &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22004",Message:message,Detail:detail} }
func (e XMLTable) OpenSource(c expr.Context) (expr.SourceCursor,error) {
 ctx,err:=evaluationContext(c)
 if err!=nil { return expr.SourceCursor{},err }
 nsCount:=len(e.Raw.Namespaces)
 document,err:=e.Args[nsCount+1].Eval(c)
 if err!=nil { return expr.SourceCursor{},err }
 if document.IsNull() { return expr.OwnedSource(e.Usage.Type,nil),nil }
 env:=c.FunctionInput()
 table,err:=sqltype.StartXMLTable(ctx,document,env)
 if err!=nil { return expr.SourceCursor{},err }
 text:=func(index int, message, detail string)(string,error){
  value,err:=e.Args[index].Eval(c)
  if err!=nil { return "",err }
  if value.IsNull() { return "",xmlTableError(message,detail) }
  return value.Text()
 }
 for i,ns:=range e.Raw.Namespaces {
  uri,err:=text(i,"namespace URI must not be null","")
  if err!=nil { return expr.SourceCursor{},err }
  if ns.Default {
   return expr.SourceCursor{},&sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"0A000",Message:"DEFAULT namespace is not supported"}
  }
  if err:=table.Namespace(ns.Name.Text,uri,env);err!=nil { return expr.SourceCursor{},err }
 }
 row,err:=text(nsCount,"row filter expression must not be null","")
 if err!=nil { return expr.SourceCursor{},err }
 if err:=table.RowFilter(row,env);err!=nil { return expr.SourceCursor{},err }
 for i,field:=range e.Fields {
  if field.Ordinality { continue }
  path:=e.Raw.Columns[i].Name.Text
  if field.Path>=0 {
   path,err=text(field.Path,"column filter expression must not be null",fmt.Sprintf("Filter for column %q is null.",e.Raw.Columns[i].Name.Text))
   if err!=nil { return expr.SourceCursor{},err }
  }
  if err:=table.ColumnFilter(path,env);err!=nil { return expr.SourceCursor{},err }
 }
 var values []sqltype.Value
 for ordinal:=int64(1);;ordinal++ {
  next,err:=table.Next(env)
  if err!=nil { return expr.SourceCursor{},err }
  if !next { break }
  fields:=make([]sqltype.Value,len(e.Fields))
  column:=0
  for i,field:=range e.Fields {
   var value sqltype.Value
   if field.Ordinality { value,err=sqltype.Integer(field.Usage.Type,ordinal) } else {
    var missing bool
    value,missing,err=table.Column(ctx,column,field.Usage,env)
    column++
    if err==nil && missing && field.Default>=0 { value,err=e.Args[field.Default].Eval(c) }
   }
   if err!=nil { return expr.SourceCursor{},err }
   if value.IsNull() && field.NotNull { return expr.SourceCursor{},xmlTableError(fmt.Sprintf("null is not allowed in column %q",e.Raw.Columns[i].Name.Text),"") }
   fields[i]=value
  }
  value,err:=sqltype.OwnedCompositeValue(e.Usage.Type,fields)
  if err!=nil { return expr.SourceCursor{},err }
  values=append(values,value)
 }
 return expr.OwnedSource(e.Usage.Type,values),nil
}
