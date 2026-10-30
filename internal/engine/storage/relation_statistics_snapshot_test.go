package storage

import (
 "encoding/base64"
 "math"
 "strings"
 "testing"
 "stuntgres/internal/engine/ids"
)

func TestRelationStatisticsMaskedMerge(t *testing.T){
 old:=&ImportedRelationStatistics{Mask:RelationStatisticsAll,Pages:-17,Tuples:21.5,AllVisible:-24,AllFrozen:-27}
 next:=MergeImportedRelationStatistics(old,ImportedRelationStatistics{Mask:RelationTuples,Tuples:22.5})
 if next==old||old.Tuples!=21.5||next.Tuples!=22.5||next.Pages != -17||next.AllVisible != -24||next.AllFrozen != -27{t.Fatal(old,next)}
 if MergeImportedRelationStatistics(old,ImportedRelationStatistics{})!=old{t.Fatal("empty delta copied immutable root")}
 cleared:=WithoutImportedRelationStatistics(next,RelationTuples)
 if cleared==next||cleared.Mask!=RelationStatisticsAll &^ RelationTuples||next.Mask!=RelationStatisticsAll{t.Fatal(cleared,next)}
 if WithoutImportedRelationStatistics(next,RelationStatisticsAll)!=nil{t.Fatal("empty override survived maintenance")}
}

func TestRelationStatisticsSnapshotCodec(t *testing.T){
 record:=RelationStatisticsSnapshot{Schema:"public",Relation:"r",Mask:RelationStatisticsAll,Pages:-17,TupleBits:math.Float32bits(21.5),AllVisible:-24,AllFrozen:-27}
 line,err:=EncodeRelationStatisticsSnapshot(record);if err!=nil{t.Fatal(err)}
 decoded,recognized,err:=DecodeRelationStatisticsSnapshot(strings.TrimPrefix(line,"-- "))
 if err!=nil||!recognized||decoded!=record{t.Fatal(decoded,recognized,err)}
 again,err:=EncodeRelationStatisticsSnapshot(decoded);if err!=nil||again!=line{t.Fatal("nondeterministic codec",again,err)}
 for _,payload:=range []string{`{"Schema":"public","Schema":"public","Relation":"r","Mask":1}`,`{"Schema":"public","Relation":"r","Mask":1,"Unknown":0}`,`null`,`{"Schema":"public","Relation":"r","Mask":16}`,`{} {}`} {
  _,recognized,err:=DecodeRelationStatisticsSnapshot(RelationStatisticsSnapshotMarker+" "+base64.StdEncoding.EncodeToString([]byte(payload)))
  if !recognized||err==nil{t.Fatal("bad JSON admitted",payload)}
 }
 for _,text:=range []string{"stuntgres-relation-statistics-v2 e30=",RelationStatisticsSnapshotMarker+" !!!"}{if _,ok,err:=DecodeRelationStatisticsSnapshot(text);!ok||err==nil{t.Fatal(text,ok,err)}}
 for _,n:=range []float32{-2,float32(math.NaN()),float32(math.Inf(1)),float32(math.Inf(-1))}{bad:=record;bad.TupleBits=math.Float32bits(n);if bad.Validate()==nil{t.Fatal("invalid tuple admitted",n)}}
 // Non-tuple records never reinterpret absent float bits as an estimate.
 record.Mask=RelationPages;record.TupleBits=math.Float32bits(float32(math.NaN()));if err=record.Validate();err!=nil{t.Fatal(err)}
}

func TestRelationStatisticsMetadataComposition(t *testing.T){
 address:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:50000}
 column:=&ColumnStatistics{Rows:7};key:=StatisticsKey{Attribute:1}
 old:=&RelationMetadata{Statistics:&RelationStatistics{Columns:map[StatisticsKey]*ColumnStatistics{key:column}},AnalyzeCount:9,Tuples:7,Scanned:true}
 imported:=&ImportedRelationStatistics{Mask:RelationTuples,Tuples:21.5}
 metadata:=map[ids.ObjectAddress]*RelationMetadata{address:old}
 merged:=MergeRelationStatisticsMetadata(metadata,map[ids.ObjectAddress]*RelationMetadata{address:{Imported:imported}})[address]
 if merged==old||old.Imported!=nil||merged.Imported!=imported||merged.Statistics!=old.Statistics||merged.AnalyzeCount!=9||merged.Tuples!=7||!merged.Scanned{t.Fatal(old,merged)}
}
