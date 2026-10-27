package exec

import (
 "fmt"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/sqltype"
 "stuntgres/internal/sqlstate"
)

func(c *Context)pageinspectInput(n plan.InstalledPageinspectExpr)(string,[]sqltype.BrinStoredColumn,bool,error){
 if err:=EvaluationContext(c);err!=nil{return "",nil,false,err}
 view:=preparation.DomainCatalog(c)
 if n.Procedure==nil || n.CallRoutineReference()!=n.Procedure || n.Ref.Address!=n.Procedure.Address() || !preparation.StoredReferenceValid(view,c.RoutineConsumer,n.Ref){return "",nil,false,fmt.Errorf("stuntgres: invalid installed pageinspect reference")}
 current,err:=ValidateStoredRoutineDescriptor(view,n.Procedure);if err!=nil{return "",nil,false,err}
 symbol,err:=extension.ProcedureSymbol(view,current);if err!=nil{return "",nil,false,err};if symbol!=n.Member{return "",nil,false,fmt.Errorf("stuntgres: invalid pageinspect member")}
 boundary:=n.Pageinspect;boundary.RoutineReference=current
 if err:=extension.ValidatePageinspectCall(view,current,boundary);err!=nil{return "",nil,false,err}
 var values [2]sqltype.Value;null:=false
 for i,child:=range n.Args{v,err:=child.Eval(c);if err!=nil{return "",nil,false,err};values[n.Slots[i]]=v;null=null||v.IsNull()}
 if null{return "",nil,true,nil}
 auth,dependencies,err:=RoutineAdmissionAuthority(c);if err!=nil{return "",nil,false,err}
 if err:=CheckRecipeRoutineDescriptor(c,auth,dependencies,n.Procedure);err!=nil{return "",nil,false,err}
 current,err=ValidateRoutineDescriptor(auth.Catalog,auth.Roles,n.Procedure);if err!=nil{return "",nil,false,err}
 n.Ref.Epoch=auth.Catalog.Epoch();n.Procedure,n.RoutineReference=current,current
 var oid uint32
 if n.Member=="brin_page_items"{oid,err=values[1].ClassOID();if err!=nil{return "",nil,false,err}}
 host:=c.LiveHost();if host==nil{return "",nil,false,fmt.Errorf("stuntgres: missing pageinspect statement runtime")}
 columns,err:=host.Pageinspect(n,oid);if err!=nil{return "",nil,false,err}
 raw,err:=values[0].Binary();return raw,columns,false,err
}
func(c *Context)pageinspect(n plan.InstalledPageinspectExpr)(sqltype.BrinPage,[]sqltype.BrinStoredColumn,bool,error){
 raw,columns,null,err:=c.pageinspectInput(n);if err!=nil || null{return sqltype.BrinPage{},columns,null,err}
 required:=uint16(0);switch n.Member{case "brin_metapage_info":required=0xf091;case "brin_revmap_data":required=0xf092;case "brin_page_items":required=0xf093}
 page,err:=sqltype.ReadBrinPage(raw,required);return page,columns,false,err
}
func(c *Context)EvalPageinspect(n plan.InstalledPageinspectExpr)(sqltype.Value,error){
 if n.Member=="fsm_page_contents"{
  raw,_,null,err:=c.pageinspectInput(n);if err!=nil{return sqltype.Value{},err};d:=n.Usage.Type
  if null{return sqltype.Null(d),nil}
  contents,isNew,err:=sqltype.ReadFSMPageContents(raw);if err!=nil{return sqltype.Value{},err}
  if isNew{return sqltype.Null(d),nil};return sqltype.String(d,contents),nil
 }
 page,_,null,err:=c.pageinspect(n);if err!=nil{return sqltype.Value{},err};d:=n.Usage.Type
 if null || page.IsNew(){return sqltype.Null(d),nil}
 if n.Member=="brin_page_type"{return sqltype.String(d,page.PageType()),nil}
 if n.Member!="brin_metapage_info"{return sqltype.Value{},fmt.Errorf("stuntgres: pageinspect set requires source invocation")}
 magic,version,width,last:=page.Meta();fields:=make([]sqltype.Value,4)
 fields[0]=sqltype.String(d.CompositeMember(0).Usage.Type,magic)
 for i,v:=range []int64{int64(version),int64(width),last}{fields[i+1],err=sqltype.Integer(d.CompositeMember(i+1).Usage.Type,v);if err!=nil{return sqltype.Value{},err}}
 return sqltype.OwnedCompositeValue(d,fields)
}
func(c *Context)OpenPageinspect(n plan.InstalledPageinspectExpr)(expr.SourceCursor,error){
 if n.Member=="bt_page_items"{return c.openBtreePageinspect(n)}
 if n.Member=="brin_page_type" || n.Member=="brin_metapage_info" || n.Member=="fsm_page_contents"{v,err:=c.EvalPageinspect(n);if err!=nil{return expr.SourceCursor{},err};return expr.ScalarSource(v),nil}
 page,columns,null,err:=c.pageinspect(n);if err!=nil{return expr.SourceCursor{},err};d:=n.Usage.Type
 if null{return expr.OwnedSource(d,nil),nil}
 if n.Member=="brin_revmap_data"{rows,err:=page.Revmap(d);if err!=nil{return expr.SourceCursor{},err};return expr.OwnedSource(d,rows),nil}
 if n.Member!="brin_page_items"{return expr.SourceCursor{},fmt.Errorf("stuntgres: scalar pageinspect requires scalar invocation")}
 items,err:=page.Items(c.Context,columns,c.Output);if err!=nil{return expr.SourceCursor{},err}
 rows:=make([]sqltype.Value,len(items))
 for i,item:=range items{
  fields:=make([]sqltype.Value,8);for j:=range fields{fields[j]=sqltype.Null(d.CompositeMember(j).Usage.Type)}
  fields[0],err=sqltype.Integer(d.CompositeMember(0).Usage.Type,int64(item.Offset));if err!=nil{return expr.SourceCursor{},err}
  if !item.Unused{
   fields[1],err=sqltype.Integer(d.CompositeMember(1).Usage.Type,item.Block);if err!=nil{return expr.SourceCursor{},err}
   fields[2],err=sqltype.Integer(d.CompositeMember(2).Usage.Type,int64(item.Attribute));if err!=nil{return expr.SourceCursor{},err}
   for j,v:=range []bool{item.AllNulls,item.HasNulls,item.Placeholder,item.Empty}{fields[j+3]=sqltype.Boolean(d.CompositeMember(j+3).Usage.Type,v)}
   if !item.AllNulls{fields[7]=sqltype.String(d.CompositeMember(7).Usage.Type,item.Value)}
  }
  rows[i],err=sqltype.OwnedCompositeValue(d,fields);if err!=nil{return expr.SourceCursor{},err}
 }
 return expr.OwnedSource(d,rows),nil
}

func(c *Context)openBtreePageinspect(n plan.InstalledPageinspectExpr)(expr.SourceCursor,error){
 raw,_,null,err:=c.pageinspectInput(n);if err!=nil{return expr.SourceCursor{},err};d:=n.Usage.Type
 if null{return expr.OwnedSource(d,nil),nil}
 page,err:=sqltype.ReadBtreePage(raw);if err!=nil{return expr.SourceCursor{},err}
 if page.IsNew(){return expr.OwnedSource(d,[]sqltype.Value{sqltype.Null(d)}),nil}
 if page.IsDeleted(){
  for _,message:=range []string{"page is deleted","page from block is deleted"}{c.LiveHost().PageinspectNotice(sqlstate.Error{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:message})}
 }
 items,err:=page.Items(c.Context);if err!=nil{return expr.SourceCursor{},err}
 rows:=make([]sqltype.Value,len(items))
 for i,item:=range items{
  fields:=make([]sqltype.Value,9);for j:=range fields{fields[j]=sqltype.Null(d.CompositeMember(j).Usage.Type)}
  for _,v:=range []struct{index int;number int32}{{0,item.Offset},{2,item.Length}}{fields[v.index],err=sqltype.Integer(d.CompositeMember(v.index).Usage.Type,int64(v.number));if err!=nil{return expr.SourceCursor{},err}}
  fields[1],err=sqltype.TIDValue(d.CompositeMember(1).Usage.Type,item.CTID.Block,item.CTID.Offset);if err!=nil{return expr.SourceCursor{},err}
  fields[3]=sqltype.Boolean(d.CompositeMember(3).Usage.Type,item.Nulls);fields[4]=sqltype.Boolean(d.CompositeMember(4).Usage.Type,item.Vars)
  fields[5]=sqltype.String(d.CompositeMember(5).Usage.Type,item.Data)
  if !item.DeadNull{fields[6]=sqltype.Boolean(d.CompositeMember(6).Usage.Type,item.Dead)}
  if !item.HeapNull{fields[7],err=sqltype.TIDValue(d.CompositeMember(7).Usage.Type,item.HeapTID.Block,item.HeapTID.Offset);if err!=nil{return expr.SourceCursor{},err}}
  if item.Posting!=nil{
   a:=d.CompositeMember(8).Usage.Type;values:=make([]sqltype.Value,len(item.Posting))
   for j,tid:=range item.Posting{values[j],err=sqltype.TIDValue(a.Element(),tid.Block,tid.Offset);if err!=nil{return expr.SourceCursor{},err}}
   dims:=[]sqltype.ArrayDimension(nil);if len(values)!=0{dims=[]sqltype.ArrayDimension{{Lower:1,Length:int32(len(values))}}}
   fields[8],err=sqltype.OwnedArrayValue(a,dims,values);if err!=nil{return expr.SourceCursor{},err}
  }
  rows[i],err=sqltype.OwnedCompositeValue(d,fields);if err!=nil{return expr.SourceCursor{},err}
 }
 return expr.OwnedSource(d,rows),nil
}
