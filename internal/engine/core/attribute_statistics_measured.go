package core

import (
 "fmt"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/storage"
 "stuntgres/internal/sqltype"
)

// Transitioning a measured row preserves its real kind occupancy as well as its
// integer backing. A scalar-only update must not make two measured slots free.
func seedAttributeStatisticsKinds(read catalog.Read,fact *storage.ColumnStatistics)error{
 if fact.Rows==0{return nil}
 valuesType,ok:=read.LookupType(2277)
 if !ok{return fmt.Errorf("stuntgres: missing statistics ANYARRAY")}
 numbersType,_:=read.LookupType(1021)
 if _,present:=fact.Imported.Slot(1);!present&&len(fact.MCV)!=0{
  values:=make([]sqltype.Value,len(fact.MCV));numbers:=make([]sqltype.Value,len(fact.MCV));for i,mcv:=range fact.MCV{values[i]=mcv.Value;numbers[i],_=sqltype.Float(numbersType.Element(),float64(mcv.Count)/float64(fact.Rows))}
  dims:=[]sqltype.ArrayDimension{{Lower:1,Length:int32(len(values))}};v,err:=sqltype.OwnedStatisticArrayValue(valuesType,fact.Usage,dims,values);if err!=nil{return err};n,err:=sqltype.OwnedArrayValue(numbersType,dims,numbers);if err!=nil{return err};if err=fact.Imported.SetSlot(storage.AttributeStatisticsSlot{Kind:1,Values:v,Numbers:n});err!=nil{return err}
 }
 if _,present:=fact.Imported.Slot(2);!present&&len(fact.Histogram)!=0{v,err:=sqltype.StatisticArrayValue(valuesType,fact.Usage,[]sqltype.ArrayDimension{{Lower:1,Length:int32(len(fact.Histogram))}},fact.Histogram);if err!=nil{return err};if err=fact.Imported.SetSlot(storage.AttributeStatisticsSlot{Kind:2,Values:v});err!=nil{return err}}
 return nil
}
