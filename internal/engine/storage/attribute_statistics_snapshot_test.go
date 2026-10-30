package storage

import (
 "context"
 "testing"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/sqltype"
)

func TestAttributeStatisticsSnapshotKindEligibility(t *testing.T) {
 types:=statisticsTypes()
 realArray,err:=sqltype.Array(1021,"_float4",types[sqltype.Float4]);if err!=nil{t.Fatal(err)}
 numbers,err:=realArray.Input(context.Background(),"{0,1,2}",-1);if err!=nil{t.Fatal(err)}
 for _,tc:=range []struct{name string;element *sqltype.Descriptor}{{"integer",types[sqltype.Int4]},{"json",types[sqltype.JSON]}}{
  array,err:=sqltype.Array(90000,"test_array",tc.element);if err!=nil{t.Fatal(err)}
  values,err:=sqltype.ParseStatisticArray(context.Background(),types[2277],tc.element.Usage(),"{1}",sqltype.InputEnvironment{});if err!=nil{t.Fatal(err)}
  for _,target:=range []*sqltype.Descriptor{tc.element,array}{
   want:=target.IsArray()&&tc.name=="integer"
   fact:=&ColumnStatistics{Usage:target.Usage()}
   for _,kind:=range []int16{4,5}{
    slotValues:=sqltype.Value{};if kind==4{slotValues=values}
    err:=validateAttributeSnapshotSlot(catalog.Read{},fact,kind,slotValues,numbers)
    if (err==nil)!=want{t.Fatalf("element kind %d on %s/array=%v: %v; admission want %v",kind,tc.name,target.IsArray(),err,want)}
   }
  }
 }
}

func TestAttributeStatisticsSnapshotRetainedUsage(t *testing.T) {
 types:=statisticsTypes();ctx:=context.Background()
 realArray,err:=sqltype.Array(1021,"_float4",types[sqltype.Float4]);if err!=nil{t.Fatal(err)}
 numbers,err:=realArray.Input(ctx,"{0.5}",-1);if err!=nil{t.Fatal(err)}
 target:=types[sqltype.VarChar].Usage();target.Typmod=7
 fact:=&ColumnStatistics{Usage:target}
 for _,typmod:=range []int32{7,-1}{
  usage:=target;usage.Typmod=typmod
  values,err:=sqltype.ParseStatisticArray(ctx,types[2277],usage,"{ab}",sqltype.InputEnvironment{});if err!=nil{t.Fatal(err)}
  err=validateAttributeSnapshotSlot(catalog.Read{},fact,1,values,numbers)
  if (err==nil)!=(typmod==7){t.Fatalf("retained typmod %d: %v",typmod,err)}
 }
}
