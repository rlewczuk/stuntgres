package session

import (
 "testing"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/storage"
)

func TestRelationStatisticsJournalOwnership(t *testing.T){
 address:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:50000}
 canonical:=&storage.RelationMetadata{Imported:&storage.ImportedRelationStatistics{Mask:storage.RelationStatisticsAll,Pages:18,Tuples:21.5,AllVisible:24,AllFrozen:27},AnalyzeCount:9}
 j:=&Journal{}
 first:=storage.ImportedRelationStatistics{Mask:storage.RelationTuples,Tuples:22.5}
 j.RecordRelationStatistics(address,nil,nil,nil,first,canonical,canonical)
 saved:=j.Clone()
 key:=storage.StatisticsKey{Attribute:1};column:=&storage.ColumnStatistics{Rows:7}
 j.RecordStatistics(address,nil,nil,nil,map[storage.StatisticsKey]*storage.ColumnStatistics{key:column},nil,nil)
 j.RecordRelationStatistics(address,nil,nil,nil,storage.ImportedRelationStatistics{Mask:storage.RelationPages,Pages:-17},canonical,nil)
 own:=ResolveRelationStatistics(j,address,canonical)
 prior:=ResolveRelationStatistics(saved,address,canonical)
 if canonical.Imported.Tuples!=21.5||canonical.Imported.Pages!=18||prior.Tuples!=22.5||prior.Pages!=18||own.Tuples!=22.5||own.Pages != -17{t.Fatal(canonical,prior,own)}
 if saved.Statistics[address].Columns[key]!=nil||j.Statistics[address].Columns[key]!=column{t.Fatal("savepoint attribute backing changed")}
 if j.Statistics[address].PrivateMetadata!=canonical{t.Fatal("later component lost private metadata")}
 j.ForgetRelationStatistics(address,storage.RelationTuples)
 own=ResolveRelationStatistics(j,address,canonical)
 if own.Tuples!=21.5||own.Pages != -17||ResolveRelationStatistics(saved,address,canonical).Tuples!=22.5{t.Fatal("maintenance mutated saved journal")}
 // Reverse recording order preserves the independently owned column component.
 reverse:=&Journal{}
 reverse.RecordStatistics(address,nil,nil,nil,map[storage.StatisticsKey]*storage.ColumnStatistics{key:column},nil,canonical)
 reverse.RecordRelationStatistics(address,nil,nil,nil,first,canonical,nil)
 if reverse.Statistics[address].Columns[key]!=column||reverse.Statistics[address].Imported!=first{t.Fatal("relation delta replaced attribute delta")}
 extendedKey:=storage.ExtendedStatisticsKey{Inherited:true}
 extended:=&storage.ExtendedStatistics{}
 reverse.RecordStatistics(address,nil,nil,nil,nil,map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics{extendedKey:extended},nil)
 reverse.RecordRelationStatistics(address,nil,nil,nil,storage.ImportedRelationStatistics{Mask:storage.RelationAllFrozen,AllFrozen:-27},canonical,nil)
 if reverse.Statistics[address].Extended[extendedKey]!=extended||reverse.Statistics[address].Columns[key]!=column||reverse.Statistics[address].Imported.Tuples!=22.5{t.Fatal("three metadata components replaced each other")}
}

func TestRelationStatisticsOrderedResetOwnership(t *testing.T){
 address:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:50000}
 canonical:=&storage.RelationMetadata{Imported:&storage.ImportedRelationStatistics{Mask:storage.RelationStatisticsAll,Pages:18,Tuples:21.5,AllVisible:24,AllFrozen:27}}
 j:=&Journal{}
 j.RecordRelationStatistics(address,nil,nil,nil,storage.ImportedRelationStatistics{Mask:storage.RelationTuples,Tuples:41.5},canonical,canonical)
 beforeReset:=j.Clone()
 defaults:=storage.ImportedRelationStatistics{Mask:storage.RelationStatisticsAll,Tuples:-1}
 j.ResetRelationStatistics(address,nil,nil,nil,storage.RelationStatisticsAll,defaults,canonical,nil)
 token:=&storage.Relation{}
 change:=j.Statistics[address];change.ImportedStorage=token;j.Statistics[address]=change
 savedReset:=j.Clone()
 if got:=ResolveRelationStatistics(j,address,canonical);got==nil||*got!=defaults{t.Fatal("owner retained fields reset by operation",got)}
 j.RecordRelationStatistics(address,nil,nil,nil,storage.ImportedRelationStatistics{Mask:storage.RelationTuples,Tuples:42.5},canonical,nil)
 key:=storage.StatisticsKey{Attribute:1};column:=&storage.ColumnStatistics{Rows:7}
 j.RecordStatistics(address,nil,nil,nil,map[storage.StatisticsKey]*storage.ColumnStatistics{key:column},nil,nil)
 if got:=ResolveRelationStatistics(j,address,canonical);got.Tuples!=42.5||got.Pages!=0||got.AllVisible!=0||got.AllFrozen!=0||j.Statistics[address].ImportedReset!=storage.RelationStatisticsAll{t.Fatal("later write lost reset or resurrected canonical bits",got)}
 if ResolveRelationStatistics(savedReset,address,canonical).Tuples != -1||ResolveRelationStatistics(beforeReset,address,canonical).Tuples!=41.5||canonical.Imported.Tuples!=21.5{t.Fatal("reset changed savepoint/canonical backing")}
 if j.Statistics[address].ImportedStorage!=token||savedReset.Statistics[address].ImportedStorage!=token||beforeReset.Statistics[address].ImportedStorage!=nil{t.Fatal("ordinary recording changed new-file identity or saved backing")}
 j.ResetRelationStatistics(address,nil,nil,nil,storage.RelationStatisticsAll,defaults,canonical,nil)
 if got:=ResolveRelationStatistics(j,address,canonical);*got!=defaults||j.Statistics[address].Columns[key]!=column{t.Fatal("second reset retained intervening writes or lost column backing",got)}
 // Reset-only tombstones suppress canonical presence even with no replacement.
 j.ResetRelationStatistics(address,nil,nil,nil,storage.RelationTuples,storage.ImportedRelationStatistics{},canonical,nil)
 if got:=ResolveRelationStatistics(j,address,canonical);got.Mask & storage.RelationTuples!=0{t.Fatal("reset-only field resurrected",got)}
}

func TestRelationStatisticsAnalyzeProvenance(t *testing.T){
 address:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:50000}
 baseline:=&storage.RelationMetadata{AnalyzeCount:1,Imported:&storage.ImportedRelationStatistics{Mask:storage.RelationStatisticsAll,Pages:18,Tuples:21.5,AllVisible:24,AllFrozen:27}}
 for _,foreign:=range []bool{false,true}{
  j:=&Journal{}
  j.RecordRelationStatistics(address,nil,nil,nil,storage.ImportedRelationStatistics{Mask:storage.RelationPages|storage.RelationTuples,Pages:17,Tuples:41.5},baseline,baseline)
  saved:=j.Clone()
  analyzed:=*baseline;analyzed.AnalyzeCount++
  replaced:=uint8(storage.RelationTuples)
  if foreign{replaced|=storage.RelationPages;analyzed.ForeignAnalyze=&storage.ForeignAnalyzeStatistics{}}
  analyzed.Imported=storage.WithoutImportedRelationStatistics(baseline.Imported,replaced)
  if got:=ResolveRelationStatistics(j,address,&analyzed);got.Mask & replaced!=0{t.Fatal("saved pre-ANALYZE writes hid later maintenance",got)}
  // A visibility-only update must prune old tuple/foreign-page writes BEFORE
  // refreshing the shared count, not bless the earlier saved values.
  j.RecordRelationStatistics(address,nil,nil,nil,storage.ImportedRelationStatistics{Mask:storage.RelationAllVisible,AllVisible:77},&analyzed,nil)
  change:=j.Statistics[address]
  if change.Imported.Mask & replaced!=0||change.ImportedAnalyzeCount!=2||change.Imported.AllVisible!=77{t.Fatal("unrelated update resurrected old maintenance-replaced fields",change)}
  if saved.Statistics[address].Imported.Tuples!=41.5||saved.Statistics[address].ImportedAnalyzeCount!=1||baseline.Imported.Tuples!=21.5{t.Fatal("maintenance resolution mutated retained backing")}
  if got:=ResolveRelationStatistics(j,address,&analyzed);got.Mask & replaced!=0||got.AllVisible!=77{t.Fatal("resolved unrelated update hid maintenance",got)}
  // A genuine later tuple restore is authoritative at the new baseline.
  j.RecordRelationStatistics(address,nil,nil,nil,storage.ImportedRelationStatistics{Mask:storage.RelationTuples,Tuples:42.5},&analyzed,nil)
  if got:=ResolveRelationStatistics(j,address,&analyzed);got.Mask & storage.RelationTuples==0||got.Tuples!=42.5{t.Fatal("post-maintenance restore was discarded",got)}
 }
}
