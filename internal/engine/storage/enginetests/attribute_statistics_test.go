package enginetests

import (
 "testing"
 "stuntgres/internal/engine/storage"
 "stuntgres/internal/sqltype"
)

func TestAttributeStatisticsPresenceAndImmutability(t *testing.T){
 var integer *sqltype.Descriptor;for _,d:=range sqltype.Builtins(){if d.ID()==sqltype.Int4{integer=d}}
 measured:=&storage.ColumnStatistics{Usage:integer.Usage(),Rows:8,NullCount:2,DistinctKnown:true,DistinctCount:3,AverageWidth:4}
 next:=storage.AttributeStatisticsCopy(measured,measured.Usage)
 if next.Rows!=8||next.NullCount!=2||next.NullFraction()!=.25{t.Fatal("measured facts lost",next)}
 next.Imported.NullFractionSet=true;next.Imported.NullFraction=.625
 next.Imported.DistinctSet=true;next.Imported.Distinct=0
 if n,known:=next.EncodedDistinct();!known||n!=0||next.NullFraction()!=.625{t.Fatal("explicit zero or imported fraction lost",n,known)}
 if measured.Imported!=nil||measured.NullFraction()!=.25||measured.DistinctCount!=3{t.Fatal("construction mutated measured root")}
 retained:=storage.AttributeStatisticsCopy(next,next.Usage);retained.Imported.NullFraction=.5
 if next.NullFraction()!=.625{t.Fatal("construction aliases imported payload")}
 key:=storage.StatisticsKey{Attribute:1};other:=storage.StatisticsKey{Attribute:1,Inherited:true}
 root:=storage.MergeStatistics(nil,map[storage.StatisticsKey]*storage.ColumnStatistics{key:next,other:measured})
 cleared:=storage.MergeStatistics(root,map[storage.StatisticsKey]*storage.ColumnStatistics{key:nil})
 if cleared.Columns[other]!=measured||root.Columns[key]!=next{t.Fatal("selected tombstone mutated or discarded unselected backing")}
}
func TestAttributeStatisticsFiveSlots(t *testing.T){
 var p storage.ImportedStatistics
 for _,kind:=range []int16{1,2,3,4,5}{if err:=p.SetSlot(storage.AttributeStatisticsSlot{Kind:kind});err!=nil{t.Fatal(err)}}
 if err:=p.SetSlot(storage.AttributeStatisticsSlot{Kind:3});err!=nil{t.Fatal("existing kind consumes another slot",err)}
 if err:=p.SetSlot(storage.AttributeStatisticsSlot{Kind:6});err==nil{t.Fatal("sixth kind accepted")}
 if _,ok:=p.Slot(6);ok{t.Fatal("failed sixth kind published")}
}
