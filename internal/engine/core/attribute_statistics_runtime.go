package core

import (
 "fmt"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/lock"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/engine/storage"
 "stuntgres/internal/engine/table"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

func(h *statementHost) AttributeStatistics(clear,variadic bool,values []sqltype.Value)(bool,error){
 var args attributeStatsArguments
 result:=true
 var err error
 if clear {
  for i,slot:=range []int{0,1,2,4}{args[slot]=values[i];if err=args.required(slot);err!=nil{return false,err}}
 } else {args,result,err=h.decodeAttributeStats(values,variadic);if err!=nil{return false,err}}
 for _,i:=range []int{0,1}{if err=args.required(i);err!=nil{return false,err}}
 schema,_:=args[0].Text();name,_:=args[1].Text()
 s:=h.scope();e:=s.Engine()
 e.Mu.Lock()
 auth,err:=s.RoutineAuthorizationLocked()
 var ref,parent ids.ObjectRef
 if err==nil{ref,err=e.ResolveRelationReference(auth,parser.ObjectName{Parts:[]parser.Identifier{{Text:schema},{Text:name}}},true)}
 if err==nil{
  relation:=auth.Catalog.MustRelation(ref.Address)
  target:=relation
  if relation.SQLIndex!=nil{target=auth.Catalog.MustRelation(relation.SQLIndex.Parent);parent=auth.View.Ref(target.Address())}
  err=attributeStatsAuthority(s,auth,target)
 }
 e.Mu.Unlock()
 if err!=nil{return false,err}
 if parent.Domain()!=nil {if err=s.AcquireSequence(parent,lock.ShareUpdateExclusive,true);err!=nil{return false,err}}
 if err=s.AcquireSequence(ref,lock.ShareUpdateExclusive,true);err!=nil{return false,err}
 e.Mu.Lock()
 auth,err=s.RoutineAuthorizationLocked()
 if err!=nil{e.Mu.Unlock();return false,err}
 relation,ok:=auth.Catalog.Relation(ref.Address)
 if !ok{e.Mu.Unlock();return false,ids.ErrCatalogReplaced}
 target:=relation;if relation.SQLIndex!=nil{target=auth.Catalog.MustRelation(relation.SQLIndex.Parent)}
 if err=attributeStatsAuthority(s,auth,target);err!=nil{e.Mu.Unlock();return false,err}
 slot,err:=attributeStatsColumn(relation,args,clear)
 if err!=nil{e.Mu.Unlock();return false,err}
 if err=args.required(4);err!=nil{e.Mu.Unlock();return false,err}
 inherited,_:=args[4].Boolean()
 key:=storage.StatisticsKey{Attribute:relation.Attributes[slot],Inherited:inherited}
 metadata:=session.ResolveRelationMetadata(s.PendingCatalog(),e.Storage.RelationMetadata,relation.Address())
 facts:=session.ResolveStatistics(s.PendingCatalog(),relation.Address(),metadata,relation,auth.Catalog)
 var old *storage.ColumnStatistics;if facts!=nil{old=facts.Columns[key]}
 read:=auth.Catalog
 e.Mu.Unlock()
 var replacement *storage.ColumnStatistics
 if !clear {
  replacement=storage.AttributeStatisticsCopy(old,relation.Columns[slot].Usage)
  if err=seedAttributeStatisticsKinds(read,replacement);err!=nil{return false,err}
  valid,updateErr:=h.updateAttributeStatistics(read,relation.Columns[slot].Name,args,replacement)
  if updateErr!=nil{return false,updateErr};result=result&&valid
 }
 e.Mu.Lock();defer e.Mu.Unlock()
 current,err:=s.RoutineAuthorizationLocked();if err!=nil{return false,err}
 now,ok:=current.Catalog.Relation(ref.Address)
 if !ok||now!=relation||current.Catalog.State()!=read.State(){return false,ids.ErrCatalogReplaced}
 target=now;if now.SQLIndex!=nil{target=current.Catalog.MustRelation(now.SQLIndex.Parent)}
 if err=attributeStatsAuthority(s,current,target);err!=nil{return false,err}
 if err=s.executionContext().Err();err!=nil{return false,err}
 if s.Catalog==nil{s.Catalog=s.PendingCatalog().Clone()}
 original,_:=e.Catalog.State.Relation(ref.Address)
 var private *storage.RelationMetadata
 if original==nil{private=metadata;if private==nil{private=&storage.RelationMetadata{}}}
 s.Catalog.RecordStatistics(ref.Address,e.Storage.Relations[ref.Address],original,now,map[storage.StatisticsKey]*storage.ColumnStatistics{key:replacement},nil,private)
 return result,nil
}

func attributeStatsAuthority(s *StatementScope,auth AuthorizationView,r *catalog.Relation)error{
 if !catalog.StatisticsRelationKind(r.RelationKind) && (r.RelationKind!=catalog.CatalogRelation||r.CatalogView||r.VirtualView) {
  return &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42809",Message:fmt.Sprintf(`cannot modify statistics for relation "%s"`,r.Name()),Detail:"This operation is not supported for "+catalog.RelationDiagnosticKind(r)+"s."}
 }
 if catalog.SharedRelation(r){return SQLError("0A000","cannot modify statistics for shared relation")}
 allowed,err:=table.VacuumAllowed(s.executionContext(),auth.View,auth,r);if err!=nil{return err}
 if !allowed{return SQLError("42501",fmt.Sprintf("permission denied for %s %s",catalog.RelationDiagnosticKind(r),r.Name()))}
 return nil
}
func attributeStatsColumn(r *catalog.Relation,a attributeStatsArguments,clear bool)(int,error){
 if a.has(2)&&a.has(3){return 0,SQLError("22023",`cannot specify both "attname" and "attnum"`)}
 if !a.has(2)&&!a.has(3){return 0,SQLError("22023",`must specify either "attname" or "attnum"`)}
 var name string
 if a.has(2){name,_=a[2].Text();if slot,ok:=r.Index[name];ok{return slot,nil}}else{
  n,_:=a[3].Integer();for slot,attribute:=range r.Attributes{if int64(attribute)==n{return slot,nil}}
  for nsystem,s:=range []string{"ctid","xmin","cmin","xmax","cmax","tableoid"}{if n==-int64(nsystem+1){name=s;break}}
  if name==""{return 0,SQLError("42703",fmt.Sprintf(`column %d of relation "%s" does not exist`,n,r.Name()))}
 }
 for _,system:=range []string{"ctid","xmin","cmin","xmax","cmax","tableoid"}{if name==system{verb:="modify statistics on";if clear{verb="clear statistics on"};return 0,SQLError("0A000",fmt.Sprintf(`cannot %s system column "%s"`,verb,name))}}
 return 0,SQLError("42703",fmt.Sprintf(`column "%s" of relation "%s" does not exist`,name,r.Name()))
}
