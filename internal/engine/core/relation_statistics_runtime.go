package core

import (
 "math"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/lock"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/engine/storage"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

func(h *statementHost) RelationStatistics(clear,variadic bool,values []sqltype.Value)(bool,error){
 var args relationStatsArguments
 result:=true
 var err error
 if clear { args[0],args[1]=values[0],values[1] } else {
  args,result,err=h.decodeRelationStats(values,variadic);if err!=nil{return false,err}
 }
 for i:=range 2 {if err=args.required(i);err!=nil{return false,err}}
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
 if parent.Domain()!=nil && (relation.SQLIndex==nil || relation.SQLIndex.Parent!=parent.Address) {e.Mu.Unlock();return false,ids.ErrCatalogReplaced}
 read:=auth.Catalog
 e.Mu.Unlock()
 var delta storage.ImportedRelationStatistics
 if clear {delta=storage.ImportedRelationStatistics{Mask:storage.RelationStatisticsAll,Tuples:-1}} else {
  for i,bit:=range []uint8{storage.RelationPages,storage.RelationTuples,storage.RelationAllVisible,storage.RelationAllFrozen}{
   slot:=i+2;if !args.has(slot){continue}
   if bit==storage.RelationTuples {
    n,_:=args[slot].Float()
    message:=""
    if math.IsNaN(n)||math.IsInf(n,0){message=`argument "reltuples" must be a finite value`}else if n < -1 {message=`argument "reltuples" must not be less than -1.0`}
    if message!=""{h.relationStatsWarning(sqlstate.Error{Code:"22023",Message:message});result=false;continue}
    delta.Tuples=float32(n)
   } else {
    n,_:=args[slot].Integer()
    switch bit {case storage.RelationPages:delta.Pages=int32(n);case storage.RelationAllVisible:delta.AllVisible=int32(n);case storage.RelationAllFrozen:delta.AllFrozen=int32(n)}
   }
   delta.Mask|=bit
  }
 }
 e.Mu.Lock();defer e.Mu.Unlock()
 current,err:=s.RoutineAuthorizationLocked();if err!=nil{return false,err}
 now,ok:=current.Catalog.Relation(ref.Address)
 if !ok||now!=relation||current.Catalog.State()!=read.State(){return false,ids.ErrCatalogReplaced}
 target=now;if now.SQLIndex!=nil{target=current.Catalog.MustRelation(now.SQLIndex.Parent)}
 if err=attributeStatsAuthority(s,current,target);err!=nil{return false,err}
 if err=s.executionContext().Err();err!=nil{return false,err}
 if delta.Mask==0{return result,nil}
 metadata:=session.ResolveRelationMetadata(s.PendingCatalog(),e.Storage.RelationMetadata,ref.Address)
 if s.Catalog==nil{s.Catalog=s.PendingCatalog().Clone()}
 original,_:=e.Catalog.State.Relation(ref.Address)
 var private *storage.RelationMetadata
 if original==nil{private=metadata;if private==nil{private=&storage.RelationMetadata{}}}
 s.Catalog.RecordRelationStatistics(ref.Address,e.Storage.Relations[ref.Address],original,relation,delta,metadata,private)
 return result,nil
}

// Actual storage replacement stages call this before their candidate publishes.
// Historical StorageChange flags cannot establish the order of later imports.
func(e *Engine) resetImportedRelationStatisticsLocked(changes *session.Journal, relation *catalog.Relation, replacement storage.ImportedRelationStatistics, newStorage *storage.Relation) {
 address:=relation.Address()
 original,_:=e.Catalog.State.Relation(address)
 metadata:=session.ResolveRelationMetadata(changes,e.Storage.RelationMetadata,address)
 current:=session.ResolveRelationStatistics(changes,address,metadata)
 // Do not manufacture imports over ordinary native maintenance estimates.
 if current==nil {replacement.Mask=0}else{replacement.Mask &= current.Mask}
 var private *storage.RelationMetadata
 if original==nil {
  private=metadata
  if private==nil {private=&storage.RelationMetadata{}}
 }
 changes.ResetRelationStatistics(address,e.Storage.Relations[address],original,relation,storage.RelationStatisticsAll,replacement,metadata,private)
 change:=changes.Statistics[address]
 change.ImportedStorage=newStorage
 changes.Statistics[address]=change
}

// PostgreSQL skips its catalog reset when the heap was created or assigned a
// new file in the current subtransaction. Retained entry journals distinguish
// that identity from ordinary statement clones and compatible heap transports.
func(s *StatementScope) truncateResetsImportedStatisticsLocked(current *session.Journal, address ids.ObjectAddress) bool {
 var entry *session.Journal
 if marks:=s.Owner.Savepoints;len(marks)!=0 {
  mark:=marks[len(marks)-1]
  entry=mark.Catalog
  if mark.Internal && mark.StatementCatalog!=nil {entry=mark.StatementCatalog}
 }
 if _,exists:=s.Engine().PrivateCatalogLocked(entry).Relation(address);!exists{return false}
 var before *storage.Relation
 if entry!=nil{before=entry.Statistics[address].ImportedStorage}
 var now *storage.Relation
 if current!=nil{now=current.Statistics[address].ImportedStorage}
 return now==before
}
