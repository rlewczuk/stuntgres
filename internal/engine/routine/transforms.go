package routine

import (
 "errors"
 "fmt"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ddl"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

// Transform-only types deliberately bypass formal/result type USAGE. The
// language admission has already run; matching pairs must already exist.
func admitRoutineTransforms(h DDLHost,auth ddl.Authority,s *parser.CreateFunction,language *catalog.Language)(*catalog.RoutineTransforms,*catalog.RoutineTransformTypes,error){
 var clause *parser.RoutineTransformClause
 for _,attribute:=range s.Attributes {
  if attribute.Kind!="transform" {continue}
  if clause!=nil {return nil,nil,expr.ErrorAt("42601","conflicting or redundant options",attribute.Pos)}
  clause=attribute.Transform
 }
 if clause==nil {return nil,nil,nil}
 entries:=make([]catalog.RoutineTransform,len(clause.Types))
 types:=make([]ids.OID,len(clause.Types))
 for i,raw:=range clause.Types {
   id,err:=h.ResolveRegtype(auth,raw)
   if err!=nil {
    var diagnostic *sqlstate.Error
    if errors.As(err,&diagnostic)&&diagnostic.Code=="42704" {
     missing:=*diagnostic
     missing.Position=""
     return nil,nil,&missing
    }
    return nil,nil,err
   }
   typ,ok:=h.DefinitionType(auth,sqltype.ID(id))
   if !ok{return nil,nil,fmt.Errorf("stuntgres: resolved transform type is unavailable")}
   // get_base_element_type strips scalar domains only while looking for an
   // array. A scalar domain itself is not normalized to its physical base.
   if base:=typ.BaseType();base.IsArray(){typ=base.Element()}
   var pair *catalog.Transform
   for _,descriptor:=range h.StageRead().Descriptors(){
    candidate,ok:=descriptor.(*catalog.Transform)
    if ok&&candidate.Language==language.Address()&&candidate.SQLType==typ {pair=candidate;break}
   }
   if pair==nil {
    path,err:=auth.ReferencePath()
    if err!=nil{return nil,nil,err}
    view:=h.StageRead()
    declaration:=view.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(typ.ID())})
    name:=catalog.TypeDiagnosticNameAt(view,nil,declaration,path)
    return nil,nil,sqlError("42704","transform for type "+name+" language \""+language.Name()+"\" does not exist")
   }
   if err:=(catalog.TransformProvider{}).Validate(h.StageRead(),pair);err!=nil{return nil,nil,err}
   entries[i]=catalog.RoutineTransform{Type:typ,Pair:pair.Address(),Revision:pair.Revision()}
   types[i]=ids.OID(typ.ID())
  }
 return catalog.NewRoutineTransforms(entries),catalog.NewRoutineTransformTypes(types),nil
}
