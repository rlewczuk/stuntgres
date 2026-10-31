package core

import (
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/engine/storage"
)

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
