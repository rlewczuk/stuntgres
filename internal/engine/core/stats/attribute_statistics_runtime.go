package stats

import (
 "fmt"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/lock"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/engine/storage"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

// ImportAttribute is pg_restore_attribute_stats, or pg_clear_attribute_stats
// when clear is set.
func ImportAttribute[A Authority](h Host[A],clear,variadic bool,values []sqltype.Value)(bool,error){
 var args attributeStatsArguments
 result:=true
 var err error
 if clear {
  for i,slot:=range []int{0,1,2,4}{args[slot]=values[i];if err=args.required(slot);err!=nil{return false,err}}
 } else {args,result,err=decodeAttributeStats(h.Operation(),values,variadic);if err!=nil{return false,err}}
 for _,i:=range []int{0,1}{if err=args.required(i);err!=nil{return false,err}}
 schema,_:=args[0].Text();name,_:=args[1].Text()
 h.ReacquireEngine()
 auth,err:=h.RoutineAuthorizationLocked()
 var ref,parent ids.ObjectRef
 if err==nil{ref,err=h.ResolveRelationLocked(auth,schema,name)}
 if err==nil{
  view:=auth.Access()
  relation:=view.Catalog.MustRelation(ref.Address)
  target:=relation
  if relation.SQLIndex!=nil{target=view.Catalog.MustRelation(relation.SQLIndex.Parent);parent=view.Ref(target.Address())}
  err=attributeStatsAuthority(h,auth,target)
 }
 h.ReleaseEngineLocked()
 if err!=nil{return false,err}
 if parent.Domain()!=nil {if err=h.AcquireObjectLock(parent,lock.ShareUpdateExclusive);err!=nil{return false,err}}
 if err=h.AcquireObjectLock(ref,lock.ShareUpdateExclusive);err!=nil{return false,err}
 h.ReacquireEngine()
 auth,err=h.RoutineAuthorizationLocked()
 if err!=nil{h.ReleaseEngineLocked();return false,err}
 read:=auth.Access().Catalog
 relation,ok:=read.Relation(ref.Address)
 if !ok{h.ReleaseEngineLocked();return false,ids.ErrCatalogReplaced}
 target:=relation;if relation.SQLIndex!=nil{target=read.MustRelation(relation.SQLIndex.Parent)}
 if err=attributeStatsAuthority(h,auth,target);err!=nil{h.ReleaseEngineLocked();return false,err}
 slot,err:=attributeStatsColumn(relation,args,clear)
 if err!=nil{h.ReleaseEngineLocked();return false,err}
 if err=args.required(4);err!=nil{h.ReleaseEngineLocked();return false,err}
 inherited,_:=args[4].Boolean()
 key:=storage.StatisticsKey{Attribute:relation.Attributes[slot],Inherited:inherited}
 metadata:=session.ResolveRelationMetadata(h.PendingCatalog(),h.EngineStorageLocked().RelationMetadata,relation.Address())
 facts:=session.ResolveStatistics(h.PendingCatalog(),relation.Address(),metadata,relation,read)
 var old *storage.ColumnStatistics;if facts!=nil{old=facts.Columns[key]}
 h.ReleaseEngineLocked()
 var replacement *storage.ColumnStatistics
 if !clear {
  replacement=storage.AttributeStatisticsCopy(old,relation.Columns[slot].Usage)
  if err=seedAttributeStatisticsKinds(read,replacement);err!=nil{return false,err}
  valid,updateErr:=updateAttributeStatistics(h,read,relation.Columns[slot].Name,args,replacement)
  if updateErr!=nil{return false,updateErr};result=result&&valid
 }
 h.ReacquireEngine();defer h.ReleaseEngineLocked()
 current,err:=h.RoutineAuthorizationLocked();if err!=nil{return false,err}
 currentRead:=current.Access().Catalog
 now,ok:=currentRead.Relation(ref.Address)
 if !ok||now!=relation||currentRead.State()!=read.State(){return false,ids.ErrCatalogReplaced}
 target=now;if now.SQLIndex!=nil{target=currentRead.MustRelation(now.SQLIndex.Parent)}
 if err=attributeStatsAuthority(h,current,target);err!=nil{return false,err}
 if err=h.Context().Err();err!=nil{return false,err}
 journal:=h.StatementCatalogLocked()
 original,_:=h.PublishedRelationLocked(ref.Address)
 var private *storage.RelationMetadata
 if original==nil{private=metadata;if private==nil{private=&storage.RelationMetadata{}}}
 journal.RecordStatistics(ref.Address,h.EngineStorageLocked().Relations[ref.Address],original,now,map[storage.StatisticsKey]*storage.ColumnStatistics{key:replacement},nil,private)
 return result,nil
}

func attributeStatsAuthority[A Authority](h Host[A],auth A,r *catalog.Relation)error{
 if !catalog.StatisticsRelationKind(r.RelationKind) && (r.RelationKind!=catalog.CatalogRelation||r.CatalogView||r.VirtualView) {
  return &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42809",Message:fmt.Sprintf(`cannot modify statistics for relation "%s"`,r.Name()),Detail:"This operation is not supported for "+catalog.RelationDiagnosticKind(r)+"s."}
 }
 if catalog.SharedRelation(r){return sqlError("0A000","cannot modify statistics for shared relation")}
 allowed,err:=h.VacuumAllowedLocked(auth,r);if err!=nil{return err}
 if !allowed{return sqlError("42501",fmt.Sprintf("permission denied for %s %s",catalog.RelationDiagnosticKind(r),r.Name()))}
 return nil
}
func attributeStatsColumn(r *catalog.Relation,a attributeStatsArguments,clear bool)(int,error){
 if a.has(2)&&a.has(3){return 0,sqlError("22023",`cannot specify both "attname" and "attnum"`)}
 if !a.has(2)&&!a.has(3){return 0,sqlError("22023",`must specify either "attname" or "attnum"`)}
 var name string
 if a.has(2){name,_=a[2].Text();if slot,ok:=r.Index[name];ok{return slot,nil}}else{
  n,_:=a[3].Integer();for slot,attribute:=range r.Attributes{if int64(attribute)==n{return slot,nil}}
  for nsystem,s:=range []string{"ctid","xmin","cmin","xmax","cmax","tableoid"}{if n==-int64(nsystem+1){name=s;break}}
  if name==""{return 0,sqlError("42703",fmt.Sprintf(`column %d of relation "%s" does not exist`,n,r.Name()))}
 }
 for _,system:=range []string{"ctid","xmin","cmin","xmax","cmax","tableoid"}{if name==system{verb:="modify statistics on";if clear{verb="clear statistics on"};return 0,sqlError("0A000",fmt.Sprintf(`cannot %s system column "%s"`,verb,name))}}
 return 0,sqlError("42703",fmt.Sprintf(`column "%s" of relation "%s" does not exist`,name,r.Name()))
}
