package core

import (
 "errors"
 "fmt"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/storage"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

// Released REL_18_6 validation order is observable through ordered notices.
func(h *statementHost) updateAttributeStatistics(read catalog.Read,name string,a attributeStatsArguments,next *storage.ColumnStatistics)(bool,error){
 result:=true
 warn:=func(code,message,detail string){h.attributeStatsWarning(sqlstate.Error{Code:code,Message:message,Detail:detail});result=false}
 doMCV,doHist,doCorr:=a.has(8)&&a.has(9),a.has(10),a.has(11)
 doElem,doCount:=a.has(12)&&a.has(13),a.has(14)
 doLength,doBounds:=a.has(15)&&a.has(16),a.has(17)
 checkArray:=func(i int)bool{
  if !a.has(i){return true};v:=a[i]
  if v.ArrayRank()!=1{warn("22023",fmt.Sprintf(`argument "%s" must not be a multidimensional array`,attributeStatsKeys[i]),"");return false}
  for j:=range v.ArrayLen(){if v.ArrayElement(j).IsNull(){warn("22023",fmt.Sprintf(`argument "%s" array must not contain null values`,attributeStatsKeys[i]),"");return false}}
  return true
 }
 if !checkArray(9){doMCV=false};if !checkArray(13){doElem=false};if !checkArray(14){doCount=false}
 pair:=func(i,j int)bool{if a.has(i)==a.has(j){return true};if !a.has(j){i,j=j,i};warn("22023",fmt.Sprintf(`argument "%s" must be specified when argument "%s" is specified`,attributeStatsKeys[i],attributeStatsKeys[j]),"");return false}
 if !pair(8,9){doMCV=false};if !pair(12,13){doElem=false};if !pair(15,16){doLength=false}
 typ:=next.Usage.Type
 var element *sqltype.Descriptor
 if doElem||doCount{
  var available bool
  element,available=storage.AttributeStatisticsElementType(read,typ)
  if !available{warn("01000",fmt.Sprintf(`could not determine element type of column "%s"`,name),"Cannot set STATISTIC_KIND_MCELEM or STATISTIC_KIND_DECHIST.");doElem,doCount=false,false}
 }
 if doHist||doCorr{available,err:=storage.AttributeStatisticsHasLessThan(read,next.Usage);if err!=nil{return false,err};if !available{warn("22023",fmt.Sprintf(`could not determine less-than operator for column "%s"`,name),"Cannot set STATISTIC_KIND_HISTOGRAM or STATISTIC_KIND_CORRELATION.");doHist,doCorr=false,false}}
 if (doLength||doBounds)&&!typ.IsRange()&&!typ.IsMultirange(){warn("22023",fmt.Sprintf(`column "%s" is not a range type`,name),"Cannot set STATISTIC_KIND_RANGE_LENGTH_HISTOGRAM or STATISTIC_KIND_BOUNDS_HISTOGRAM.");doLength,doBounds=false,false}
 if a.has(5){n,_:=a[5].Float();next.Imported.NullFractionSet=true;next.Imported.NullFraction=float32(n)}
 if a.has(6){n,_:=a[6].Integer();next.AverageWidth=int32(n)}
 if a.has(7){n,_:=a[7].Float();next.Imported.DistinctSet=true;next.Imported.Distinct=float32(n)}
 numbers:=func(value sqltype.Value)(sqltype.Value,error){d,_:=read.LookupType(1021);return sqltype.ArrayValue(d,[]sqltype.ArrayDimension{{Lower:1,Length:1}},[]sqltype.Value{value})}
 parse:=func(i int,element *sqltype.Descriptor)(sqltype.Value,bool,error){
  d,ok:=read.LookupType(2277);if !ok{return sqltype.Value{},false,fmt.Errorf("stuntgres: statistics ANYARRAY type unavailable")}
  usage:=element.Usage();usage.Typmod=next.Usage.Typmod;if element==next.Usage.Type{usage=next.Usage}
  text,_:=a[i].Text();v,err:=sqltype.ParseStatisticArray(h.Context(),d,usage,text,h.Input)
  if err!=nil{var diagnostic *sqlstate.Error;if sqltype.InputSoftError(err)&&errors.As(err,&diagnostic){h.attributeStatsWarning(*diagnostic);result=false;return sqltype.Value{},false,nil};return sqltype.Value{},false,err}
  if v.ArrayRank()!=1{warn("22023",fmt.Sprintf(`"%s" must be a one-dimensional array`,attributeStatsKeys[i]),"");return sqltype.Value{},false,nil}
  for j:=range v.ArrayLen(){if v.ArrayElement(j).IsNull(){warn("22023",fmt.Sprintf(`"%s" array must not contain null values`,attributeStatsKeys[i]),"");return sqltype.Value{},false,nil}}
  return v,true,nil
 }
 set:=func(kind int16,values,numbers sqltype.Value)error{return next.Imported.SetSlot(storage.AttributeStatisticsSlot{Kind:kind,Values:values,Numbers:numbers})}
 if doMCV{v,ok,err:=parse(8,typ);if err!=nil{return false,err};if ok{if v.ArrayLen()!=a[9].ArrayLen(){warn("22023",`could not parse "most_common_vals": incorrect number of elements (same as "most_common_freqs" required)`,"")}else if err=set(1,v,a[9]);err!=nil{return false,err}}}
 if doHist{v,ok,err:=parse(10,typ);if err!=nil{return false,err};if ok{if err=set(2,v,sqltype.Value{});err!=nil{return false,err}}}
 if doCorr{n,err:=numbers(a[11]);if err!=nil{return false,err};if err=set(3,sqltype.Value{},n);err!=nil{return false,err}}
 if doElem{v,ok,err:=parse(12,element);if err!=nil{return false,err};if ok{if err=set(4,v,a[13]);err!=nil{return false,err}}}
 if doCount{if err:=set(5,sqltype.Value{},a[14]);err!=nil{return false,err}}
 if doBounds{bounds:=typ;if typ.IsMultirange(){bounds=typ.RangeType()};v,ok,err:=parse(17,bounds);if err!=nil{return false,err};if ok{if err=set(7,v,sqltype.Value{});err!=nil{return false,err}}}
 if doLength{float8,_:=read.LookupType(sqltype.Float8);v,ok,err:=parse(15,float8);if err!=nil{return false,err};if ok{n,err:=numbers(a[16]);if err!=nil{return false,err};if err=set(6,v,n);err!=nil{return false,err}}}
 return result,nil
}
