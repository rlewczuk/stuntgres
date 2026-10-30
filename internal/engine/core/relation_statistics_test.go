package core_test

import (
 "context"
 "math"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/storage"
)

func TestRelationStatisticsPhysicalIndependence(t *testing.T){
 e:=engine.New();s:=enginetest.StartSession(t,e,nil)
 enginetest.SessionSQL(t,s,"CREATE TABLE rst_unit(a int)")
 enginetest.SessionSQL(t,s,"INSERT INTO rst_unit VALUES(7),(8)")
 address:=e.CatalogRead().NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.PublicNamespaceOID},Name:"rst_unit"})
 store:=e.Storage.Relations[address];root:=store.Versions;ends:=store.Ends;generation:=store.Generation;maintenance:=store.Maintenance
 row,version:=e.Storage.RowFrontier(),e.Storage.VersionFrontier()
 enginetest.SessionSQL(t,s,"SELECT pg_restore_relation_stats('schemaname','public','relname','rst_unit','relpages',-17,'reltuples',21.5::real,'relallvisible',-24,'relallfrozen',-27)")
 imported:=e.Storage.RelationMetadata[address].Imported
 if imported==nil||imported.Mask!=storage.RelationStatisticsAll||imported.Pages != -17||math.Float32bits(imported.Tuples)!=math.Float32bits(21.5)||imported.AllVisible != -24||imported.AllFrozen != -27{t.Fatal(imported)}
 if e.Storage.Relations[address]!=store||!storage.SameVersionRoot(root,store.Versions)||store.Ends!=ends||store.Generation!=generation||store.Maintenance!=maintenance||e.Storage.RowFrontier()!=row||e.Storage.VersionFrontier()!=version{t.Fatal("logical estimates rewrote physical rows/frontiers/counters")}
 enginetest.SessionSQL(t,s,"BEGIN")
 enginetest.SessionSQL(t,s,"SELECT pg_restore_relation_stats('schemaname','public','relname','rst_unit','reltuples',22.5::real)")
 if e.Storage.RelationMetadata[address].Imported!=imported{t.Fatal("private delta mutated canonical root")}
 enginetest.SessionSQL(t,s,"SAVEPOINT rst_unit_save")
 enginetest.SessionSQL(t,s,"SELECT pg_clear_relation_stats('public','rst_unit')")
 enginetest.SessionSQL(t,s,"ROLLBACK TO rst_unit_save")
 enginetest.SessionSQL(t,s,"COMMIT")
 next:=e.Storage.RelationMetadata[address].Imported
 if next==imported||next.Tuples!=22.5||next.Pages != -17||imported.Tuples!=21.5{t.Fatal("immutable publication/savepoint lost",imported,next)}
 enginetest.SessionSQL(t,s,"BEGIN")
 enginetest.SessionSQL(t,s,"SELECT pg_restore_relation_stats('schemaname','public','relname','rst_unit','reltuples',23.5::real)")
 enginetest.SessionSQL(t,s,"PREPARE TRANSACTION 'rst_unit_prepared'")
 if e.Storage.RelationMetadata[address].Imported!=next{t.Fatal("PREPARE published or mutated retained metadata")}
 enginetest.SessionSQL(t,s,"ROLLBACK PREPARED 'rst_unit_prepared'")
 if e.Storage.RelationMetadata[address].Imported!=next{t.Fatal("aborted prepared completion changed canonical ownership")}
 enginetest.SessionSQL(t,s,"BEGIN")
 enginetest.SessionSQL(t,s,"SELECT pg_restore_relation_stats('schemaname','public','relname','rst_unit','reltuples',24.5::real)")
 enginetest.SessionSQL(t,s,"PREPARE TRANSACTION 'rst_unit_prepared'")
 enginetest.SessionSQL(t,s,"COMMIT PREPARED 'rst_unit_prepared'")
 if e.Storage.RelationMetadata[address].Imported.Tuples!=24.5||next.Tuples!=22.5||store.Maintenance!=maintenance{t.Fatal("prepared publication mutated old backing or physical counters")}
 enginetest.SessionSQL(t,s,"BEGIN")
 enginetest.SessionSQL(t,s,"CREATE TABLE rst_unit_parent(a int) PARTITION BY RANGE(a)")
 enginetest.SessionSQL(t,s,"SELECT pg_restore_relation_stats('schemaname','public','relname','rst_unit_parent','reltuples',31.5::real)")
 enginetest.SessionSQL(t,s,"COMMIT")
 for _,d:=range e.CatalogRead().Descriptors(){r,ok:=d.(*catalog.Relation);if ok&&r.Name()=="rst_unit_parent"{if e.Storage.Relations[r.Address()]!=nil||e.Storage.RelationMetadata[r.Address()]==nil||e.Storage.RelationMetadata[r.Address()].Imported.Tuples!=31.5{t.Fatal("metadata-only relation acquired heap or lost statistics")}}}
}

func TestRelationStatisticsSymbolicSnapshotRemap(t *testing.T){
 first:=engine.New();a:=enginetest.StartSession(t,first,nil)
 enginetest.SessionSQL(t,a,"CREATE TABLE rst_remap(a int)")
 enginetest.SessionSQL(t,a,"SELECT pg_restore_relation_stats('schemaname','public','relname','rst_remap','reltuples',21.5::real)")
 records,err:=storage.CaptureRelationStatisticsSnapshots(context.Background(),first.CatalogRead(),first.Storage.RelationMetadata);if err!=nil{t.Fatal(err)}
 second:=engine.New();b:=enginetest.StartSession(t,second,nil)
 enginetest.SessionSQL(t,b,"CREATE TABLE rst_padding(a int)")
 enginetest.SessionSQL(t,b,"CREATE TABLE rst_remap(a int)")
 restored,err:=storage.RestoreRelationStatisticsSnapshots(context.Background(),second.CatalogRead(),records);if err!=nil{t.Fatal(err)}
 if len(restored)!=1{t.Fatal(restored)}
 for address,entry:=range restored{r:=second.CatalogRead().MustRelation(address);if r.Name()!="rst_remap"||entry.Imported.Tuples!=21.5{t.Fatal(r,entry)}}
 if _,err=storage.RestoreRelationStatisticsSnapshots(context.Background(),second.CatalogRead(),append(records,records...));err==nil{t.Fatal("duplicate symbolic snapshot admitted")}
}

func TestRelationStatisticsResetPublicationOwnership(t *testing.T){
 e:=engine.New();s:=enginetest.StartSession(t,e,nil)
 enginetest.SessionSQL(t,s,"CREATE TABLE rst_order_unit(a int)")
 enginetest.SessionSQL(t,s,"INSERT INTO rst_order_unit VALUES(7),(8)")
 enginetest.SessionSQL(t,s,"SELECT pg_restore_relation_stats('schemaname','public','relname','rst_order_unit','relpages',18,'reltuples',21.5::real,'relallvisible',24,'relallfrozen',27)")
 read:=e.CatalogRead();namespace:=read.NameAddress(ids.NameKey{ClassID:ids.NamespaceClassID,Name:"public"})
 address:=read.NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:namespace,Name:"rst_order_unit"})
 canonical:=e.Storage.RelationMetadata[address];oldRows:=e.Storage.Relations[address].Versions
 enginetest.SessionSQL(t,s,"BEGIN")
 enginetest.SessionSQL(t,s,"TRUNCATE rst_order_unit")
 journal:=s.Transactions.Catalog;reset:=journal.Statistics[address]
 if reset.ImportedReset!=storage.RelationStatisticsAll||reset.Imported.Mask!=storage.RelationStatisticsAll||reset.Imported.Tuples != -1||reset.ImportedStorage!=journal.Storage[address].Replacement||e.Storage.RelationMetadata[address]!=canonical{t.Fatal("reset staged outside immutable journal authority",reset)}
 enginetest.SessionSQL(t,s,"SAVEPOINT rst_order_save")
 enginetest.SessionSQL(t,s,"SELECT pg_restore_relation_stats('schemaname','public','relname','rst_order_unit','reltuples',42.5::real)")
 later:=s.Transactions.Catalog.Statistics[address]
 if reset.Imported.Tuples != -1||later.Imported.Tuples!=42.5||later.ImportedReset!=reset.ImportedReset||later.ImportedStorage!=reset.ImportedStorage{t.Fatal("later write mutated reset backing",reset,later)}
 enginetest.SessionSQL(t,s,"ROLLBACK TO rst_order_save")
 if restored:=s.Transactions.Catalog.Statistics[address];restored.Imported!=reset.Imported||restored.ImportedReset!=reset.ImportedReset||restored.ImportedStorage!=reset.ImportedStorage{t.Fatal("savepoint lost reset tombstone/token")}
 enginetest.SessionSQL(t,s,"SELECT pg_restore_relation_stats('schemaname','public','relname','rst_order_unit','reltuples',43.5::real)")
 owned:=s.Transactions.Catalog
 enginetest.SessionSQL(t,s,"PREPARE TRANSACTION 'rst_order_prepared'")
 prepared:=core.EnginePreparedRef(e).Lookup("rst_order_prepared")
 if prepared==nil||prepared.Catalog.Statistics[address].Imported!=owned.Statistics[address].Imported||prepared.Catalog.Statistics[address].ImportedReset!=storage.RelationStatisticsAll||prepared.Catalog.Statistics[address].ImportedStorage!=reset.ImportedStorage||e.Storage.RelationMetadata[address]!=canonical{t.Fatal("prepared owner lost ordered reset/write/token delta")}
 enginetest.SessionSQL(t,s,"COMMIT PREPARED 'rst_order_prepared'")
 current:=e.Storage.RelationMetadata[address].Imported
 if current.Tuples!=43.5||current.Pages!=0||current.AllVisible!=0||current.AllFrozen!=0||canonical.Imported.Tuples!=21.5||!storage.SameVersionRoot(oldRows,owned.Storage[address].Root){t.Fatal("completion lost later write or mutated retained roots",current)}
}

func TestRelationStatisticsInternalResetEntryOwnership(t *testing.T){
 e:=engine.New();s:=enginetest.StartSession(t,e,nil)
 enginetest.SessionSQL(t,s,"CREATE TABLE rst_internal_unit(a integer)")
 enginetest.SessionSQL(t,s,"SELECT pg_restore_relation_stats('schemaname','public','relname','rst_internal_unit','reltuples',21.5::real)")
 enginetest.SessionSQL(t,s,`CREATE FUNCTION rst_internal_reset() RETURNS void LANGUAGE plpgsql AS $$
 BEGIN
  TRUNCATE rst_internal_unit;
  PERFORM pg_restore_relation_stats('schemaname','public','relname','rst_internal_unit','reltuples',44.5::real);
  BEGIN
   TRUNCATE rst_internal_unit;
  EXCEPTION WHEN OTHERS THEN
   RAISE;
  END;
 END
 $$`)
 read:=e.CatalogRead();ns:=read.NameAddress(ids.NameKey{ClassID:ids.NamespaceClassID,Name:"public"})
 address:=read.NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:ns,Name:"rst_internal_unit"})
 canonical:=e.Storage.RelationMetadata[address]
 enginetest.SessionSQL(t,s,"BEGIN")
 enginetest.SessionSQL(t,s,"SELECT rst_internal_reset()")
 journal:=s.Transactions.Catalog
 change:=journal.Statistics[address]
 if change.Imported.Tuples != -1||change.ImportedReset!=storage.RelationStatisticsAll||change.ImportedStorage!=journal.Storage[address].Replacement||e.Storage.RelationMetadata[address]!=canonical||len(s.Transactions.Savepoints)!=0{t.Fatal("internal entry ignored statement journal or leaked ownership",change)}
 retained:=journal.Clone()
 enginetest.SessionSQL(t,s,"SELECT pg_restore_relation_stats('schemaname','public','relname','rst_internal_unit','reltuples',45.5::real)")
 enginetest.SessionSQL(t,s,"TRUNCATE rst_internal_unit")
 later:=s.Transactions.Catalog.Statistics[address]
 if later.Imported.Tuples!=45.5||later.ImportedStorage!=change.ImportedStorage||retained.Statistics[address].Imported.Tuples != -1{t.Fatal("released child token did not become parent-owned",change,later)}
 enginetest.SessionSQL(t,s,"ROLLBACK")
 if e.Storage.RelationMetadata[address]!=canonical||canonical.Imported.Tuples!=21.5{t.Fatal("internal rollback changed canonical ownership")}
}
