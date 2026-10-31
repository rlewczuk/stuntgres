package exec

import (
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/sqltype"
)

func(c *Context)evalGinPageinspect(n plan.InstalledPageinspectExpr)(sqltype.Value,error) {
 input,err:=c.pageinspectInput(n);if err!=nil {return sqltype.Value{},err}
 d:=n.Usage.Type;if input.null {return sqltype.Null(d),nil}
 kind:=sqltype.GinMetapage;if n.Member=="gin_page_opaque_info" {kind=sqltype.GinOpaquePage}
 page,err:=sqltype.ReadGinPage(input.raw,kind);if err!=nil {return sqltype.Value{},err}
 if page.IsNew() {return sqltype.Null(d),nil}
 fields:=make([]sqltype.Value,d.CompositeLen())
 if kind==sqltype.GinMetapage {
  for i,number:=range page.Meta() {fields[i],err=sqltype.Integer(d.CompositeMember(i).Usage.Type,number);if err!=nil {return sqltype.Value{},err}}
 } else {
  rightlink,maxoff,flags:=page.Opaque()
  for i,number:=range [...]int64{int64(rightlink),int64(maxoff)} {fields[i],err=sqltype.Integer(d.CompositeMember(i).Usage.Type,number);if err!=nil {return sqltype.Value{},err}}
  a:=d.CompositeMember(2).Usage.Type;values:=make([]sqltype.Value,len(flags))
  for i,flag:=range flags {values[i]=sqltype.String(a.Element(),flag)}
  var dims []sqltype.ArrayDimension;if len(values)!=0 {dims=[]sqltype.ArrayDimension{{Lower:1,Length:int32(len(values))}}}
  fields[2],err=sqltype.OwnedArrayValue(a,dims,values);if err!=nil {return sqltype.Value{},err}
 }
 return sqltype.OwnedCompositeValue(d,fields)
}
func(c *Context)openGinPageinspect(n plan.InstalledPageinspectExpr)(expr.SourceCursor,error) {
 input,err:=c.pageinspectInput(n);if err!=nil {return expr.SourceCursor{},err}
 d:=n.Usage.Type;if input.null {return expr.OwnedSource(d,nil),nil}
 page,err:=sqltype.ReadGinPage(input.raw,sqltype.GinLeafPage);if err!=nil {return expr.SourceCursor{},err}
 if page.IsNew() {return expr.OwnedSource(d,[]sqltype.Value{sqltype.Null(d)}),nil}
 items,err:=page.LeafItems(c.Context);if err!=nil {return expr.SourceCursor{},err}
 rows:=make([]sqltype.Value,len(items))
 for i,item:=range items {
  fields:=make([]sqltype.Value,3)
  fields[0],err=sqltype.TIDValue(d.CompositeMember(0).Usage.Type,item.First.Block,item.First.Offset);if err!=nil {return expr.SourceCursor{},err}
  fields[1],err=sqltype.Integer(d.CompositeMember(1).Usage.Type,int64(item.NBytes));if err!=nil {return expr.SourceCursor{},err}
  a:=d.CompositeMember(2).Usage.Type;values:=make([]sqltype.Value,len(item.TIDs))
  for j,tid:=range item.TIDs {values[j],err=sqltype.TIDValue(a.Element(),tid.Block,tid.Offset);if err!=nil {return expr.SourceCursor{},err}}
  fields[2],err=sqltype.OwnedArrayValue(a,[]sqltype.ArrayDimension{{Lower:1,Length:int32(len(values))}},values);if err!=nil {return expr.SourceCursor{},err}
  rows[i],err=sqltype.OwnedCompositeValue(d,fields);if err!=nil {return expr.SourceCursor{},err}
 }
 return expr.OwnedSource(d,rows),nil
}
