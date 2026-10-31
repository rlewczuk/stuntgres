package stats

import (
 "math"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/lock"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/engine/storage"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

// ImportRelation is pg_restore_relation_stats, or pg_clear_relation_stats
// when clear is set.
func ImportRelation[A Authority](h Host[A],clear,variadic bool,values []sqltype.Value)(bool,error){
 var args relationStatsArguments
 result:=true
 var err error
 if clear { args[0],args[1]=values[0],values[1] } else {
  args,result,err=decodeRelationStats(h.Operation(),values,variadic);if err!=nil{return false,err}
 }
 for i:=range 2 {if err=args.required(i);err!=nil{return false,err}}
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
 if parent.Domain()!=nil && (relation.SQLIndex==nil || relation.SQLIndex.Parent!=parent.Address) {h.ReleaseEngineLocked();return false,ids.ErrCatalogReplaced}
 h.ReleaseEngineLocked()
 var delta storage.ImportedRelationStatistics
 if clear {delta=storage.ImportedRelationStatistics{Mask:storage.RelationStatisticsAll,Tuples:-1}} else {
  for i,bit:=range []uint8{storage.RelationPages,storage.RelationTuples,storage.RelationAllVisible,storage.RelationAllFrozen}{
   slot:=i+2;if !args.has(slot){continue}
   if bit==storage.RelationTuples {
    n,_:=args[slot].Float()
    message:=""
    if math.IsNaN(n)||math.IsInf(n,0){message=`argument "reltuples" must be a finite value`}else if n < -1 {message=`argument "reltuples" must not be less than -1.0`}
    if message!=""{relationStatsWarning(h.Operation(),sqlstate.Error{Code:"22023",Message:message});result=false;continue}
    delta.Tuples=float32(n)
   } else {
    n,_:=args[slot].Integer()
    switch bit {case storage.RelationPages:delta.Pages=int32(n);case storage.RelationAllVisible:delta.AllVisible=int32(n);case storage.RelationAllFrozen:delta.AllFrozen=int32(n)}
   }
   delta.Mask|=bit
  }
 }
 h.ReacquireEngine();defer h.ReleaseEngineLocked()
 current,err:=h.RoutineAuthorizationLocked();if err!=nil{return false,err}
 currentRead:=current.Access().Catalog
 now,ok:=currentRead.Relation(ref.Address)
 if !ok||now!=relation||currentRead.State()!=read.State(){return false,ids.ErrCatalogReplaced}
 target=now;if now.SQLIndex!=nil{target=currentRead.MustRelation(now.SQLIndex.Parent)}
 if err=attributeStatsAuthority(h,current,target);err!=nil{return false,err}
 if err=h.Context().Err();err!=nil{return false,err}
 if delta.Mask==0{return result,nil}
 metadata:=session.ResolveRelationMetadata(h.PendingCatalog(),h.EngineStorageLocked().RelationMetadata,ref.Address)
 journal:=h.StatementCatalogLocked()
 original,_:=h.PublishedRelationLocked(ref.Address)
 var private *storage.RelationMetadata
 if original==nil{private=metadata;if private==nil{private=&storage.RelationMetadata{}}}
 journal.RecordRelationStatistics(ref.Address,h.EngineStorageLocked().Relations[ref.Address],original,relation,delta,metadata,private)
 return result,nil
}
