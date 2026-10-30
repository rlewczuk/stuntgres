package core

import (
 "context"
 "errors"
 "testing"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/dml"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/engine/storage"
)

// This is a pure activation-owner unit: no composed engine or SQL response is
// simulated. Public/internal composed witnesses exercise real nested utilities.
func TestRelationStatisticsTruncateScratchOwnership(t *testing.T){
 for _,primary:=range []error{nil,errors.New("nested utility failure"),context.Canceled}{
  outer:=&dml.TruncateScope{Relations:make([]dml.TruncateRelation,1),Selected:make(map[ids.ObjectAddress]int)}
  address:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:50000}
  outer.Selected[address]=1
  token:=&storage.Relation{}
  journal:=session.NewJournal()
  journal.RecordRelationStatistics(address,nil,nil,nil,storage.ImportedRelationStatistics{Mask:storage.RelationTuples,Tuples:21.5},nil,nil)
  change:=journal.Statistics[address];change.ImportedStorage=token;journal.Statistics[address]=change
  s:=&StatementScope{Catalog:journal,Owner:&session.TransactionOwner{},truncateSlot:outer}
  restore:=s.ActivateSQLRoutine(nil,catalog.Read{})
  if s.truncateSlot!=nil||s.Catalog!=journal{t.Fatal("child inherited utility admission or lost live owner journal")}
  child:=&dml.TruncateScope{Relations:make([]dml.TruncateRelation,2)}
  s.truncateSlot=child
  innerRestore:=s.ActivateSQLRoutine(nil,catalog.Read{})
  if s.truncateSlot!=nil{t.Fatal("nested child inherited parent manifest")}
  if err:=innerRestore(nil);err!=nil||s.truncateSlot!=child{t.Fatal("inner restoration lost exact child owner",err)}
  s.Catalog.RecordRelationStatistics(address,nil,nil,nil,storage.ImportedRelationStatistics{Mask:storage.RelationTuples,Tuples:42.5},nil,nil)
  if err:=restore(primary);err!=primary{t.Fatal("activation replaced primary outcome",err,primary)}
  if s.truncateSlot!=outer||len(outer.Relations)!=1||outer.Selected[address]!=1||len(child.Relations)!=2{t.Fatal("outer/child admission identity or contents changed")}
  if s.Catalog!=journal||journal.Statistics[address].Imported.Tuples!=42.5||journal.Statistics[address].ImportedStorage!=token{t.Fatal("lexical restore discarded real journal effects/provenance")}
  // Transaction rotation must not resurrect predecessor utility admission.
  rebased:=s.rebaseRoutineScratch(s.sqlRoutineScratch())
  s.installSQLRoutineScratch(rebased)
  if s.truncateSlot!=nil||s.Catalog!=journal{t.Fatal("rebase restored obsolete utility admission or replaced journal owner")}
 }
}
