package storage

import (
 "context"
 "fmt"
 "slices"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/sqltype"
)

// ReownRetainedColumnStatistics follows the destination's TEMP retention
// decision. Catalog-result ANYARRAY is not a stored column dependency, so its
// outer descriptor must be reminted even when the retained element is unchanged.
func ReownRetainedColumnStatistics(ctx context.Context,fact *ColumnStatistics,usage sqltype.Usage,oldRead,read catalog.Read,source,destination StatisticsSnapshotCodec)(*ColumnStatistics,error){
 next:=*fact;next.Usage=usage;changed:=fact.Usage!=usage
 remap:=func(v sqltype.Value,old,new sqltype.Usage)(sqltype.Value,error){
  if old==new&&!old.Type.ContainsCatalogIdentifier(){return v,nil}
  raw,err:=source.Encode(old,v);if err!=nil{return sqltype.Value{},err};return destination.Decode(new,raw)
 }
 if changed||usage.Type.ContainsCatalogIdentifier(){
  next.MCV=slices.Clone(fact.MCV);for i:=range next.MCV{v,err:=remap(fact.MCV[i].Value,fact.Usage,usage);if err!=nil{return nil,err};next.MCV[i].Value=v}
  next.Histogram=slices.Clone(fact.Histogram);for i:=range next.Histogram{v,err:=remap(fact.Histogram[i],fact.Usage,usage);if err!=nil{return nil,err};next.Histogram[i]=v};changed=true
 }
 if fact.Imported!=nil{
  imported:=*fact.Imported
  for i,slot:=range imported.Slots{
   if slot.Kind==0{continue}
   if slot.Values.Type()!=nil{
    oldUsage,ok:=slot.Values.StatisticArrayUsage();if !ok{return nil,fmt.Errorf("stuntgres: invalid retained statistics values")}
    newUsage,err:=attributeSnapshotUsage(read,ndistinctUsageRecord(oldRead,oldUsage));if err!=nil{return nil,err}
    target,ok:=read.LookupType(2277);if !ok{return nil,fmt.Errorf("stuntgres: missing retained ANYARRAY")}
    if oldUsage==newUsage&&!oldUsage.Type.ContainsCatalogIdentifier(){slot.Values,err=slot.Values.MapStatisticArray(target,nil)}else{
     slot.Values,err=slot.Values.MapStatisticArray(target,func(v sqltype.Value,_ sqltype.Usage)(sqltype.Value,sqltype.Usage,error){if v.IsNull(){return sqltype.Null(newUsage.Type),newUsage,nil};mapped,err:=remap(v,oldUsage,newUsage);return mapped,newUsage,err})
    }
    if err!=nil{return nil,err}
   }
   if slot.Numbers.Type()!=nil{target,ok:=read.LookupType(slot.Numbers.Type().ID());if !ok{return nil,fmt.Errorf("stuntgres: missing retained statistics numbers")};v,err:=slot.Numbers.Retag(target);if err!=nil{return nil,err};slot.Numbers=v}
   if slot!=imported.Slots[i]{imported.Slots[i]=slot;changed=true}
  }
  if changed{next.Imported=&imported}
 }
 if err:=ctx.Err();err!=nil{return nil,err};if !changed{return fact,nil};return &next,nil
}
