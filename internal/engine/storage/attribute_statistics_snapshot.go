package storage

import (
 "bytes"
 "context"
 "encoding/base64"
 "encoding/json"
 "fmt"
 "io"
 "math"
 "slices"
 "strings"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

const AttributeStatisticsSnapshotMarker="stuntgres-attribute-statistics-v1"
type AttributeStatisticsValueSnapshot struct{Usage NDistinctUsageRecord `json:"usage"`;Statistic bool `json:"statistic"`;Dimensions []sqltype.ArrayDimension `json:"dimensions"`;Elements []json.RawMessage `json:"elements"`}
type AttributeStatisticsSlotSnapshot struct{Kind int16 `json:"kind"`;Values *AttributeStatisticsValueSnapshot `json:"values,omitempty"`;Numbers *AttributeStatisticsValueSnapshot `json:"numbers,omitempty"`}
type AttributeStatisticsSnapshot struct{
 Schema,Relation,Column string
 Attribute int32
 Inherited bool
 Hierarchy bool
 Usage NDistinctUsageRecord
 Rows,NullCount,DistinctCount uint64
 DistinctKnown bool
 DistinctOverride uint64
 AverageWidth int32
 MCV []StatisticsMCVSnapshot
 Histogram []json.RawMessage
 Imported bool
 NullFractionSet,DistinctSet bool
 NullFraction,Distinct uint32
 Slots []AttributeStatisticsSlotSnapshot
}
func(r AttributeStatisticsSnapshot) Validate()error{
 invalid:=func()error{return fmt.Errorf("stuntgres: invalid attribute statistics snapshot")}
 if r.Schema==""||r.Relation==""||r.Column==""||r.Attribute<=0||r.Usage.TypeName==""||r.Usage.TypeSchema==""||r.NullCount>r.Rows||r.DistinctCount>r.Rows-r.NullCount||len(r.Slots)>5{return invalid()}
 if !r.Imported&&(r.NullFractionSet||r.DistinctSet||len(r.Slots)!=0){return invalid()}
 var seen [8]bool
 for _,slot:=range r.Slots{if slot.Kind<1||slot.Kind>7||seen[slot.Kind]{return invalid()};seen[slot.Kind]=true
  values:=slot.Kind==1||slot.Kind==2||slot.Kind==4||slot.Kind==6||slot.Kind==7
  numbers:=slot.Kind==1||slot.Kind==3||slot.Kind==4||slot.Kind==5||slot.Kind==6
  if values!=(slot.Values!=nil)||numbers!=(slot.Numbers!=nil){return invalid()}
  for _,v:=range []*AttributeStatisticsValueSnapshot{slot.Values,slot.Numbers}{if v!=nil{if v.Usage.TypeSchema==""||v.Usage.TypeName==""||len(v.Dimensions)!=1||len(v.Elements)==0{return invalid()};for _,raw:=range v.Elements{if len(raw)==0||bytes.Equal(raw,[]byte("null")){return invalid()}}}}
  if slot.Values!=nil&&!slot.Values.Statistic||slot.Numbers!=nil&&slot.Numbers.Statistic{return invalid()}
 }
 for _,v:=range r.MCV{if v.Count==0||v.Count>r.Rows||len(v.Value)==0{return invalid()}}
 return nil
}
func CaptureAttributeStatisticsSnapshots(ctx context.Context,read catalog.Read,metadata map[ids.ObjectAddress]*RelationMetadata,codec StatisticsSnapshotCodec)([]AttributeStatisticsSnapshot,error){
 var records []AttributeStatisticsSnapshot
 encode:=func(v sqltype.Value)(*AttributeStatisticsValueSnapshot,error){
  if v.Type()==nil{return nil,nil}
  usage,statistic:=v.StatisticArrayUsage();if !statistic{usage=v.Type().Element().Usage()}
  r:=&AttributeStatisticsValueSnapshot{Usage:ndistinctUsageRecord(read,usage),Statistic:statistic,Dimensions:v.ArrayDimensions(),Elements:make([]json.RawMessage,v.ArrayLen())}
  for i:=range r.Elements{raw,err:=codec.Encode(usage,v.ArrayElement(i));if err!=nil{return nil,err};r.Elements[i]=raw}
  return r,nil
 }
 for address,entry:=range metadata{
  relation,ok:=read.Relation(address);if !ok||entry==nil{continue};ns:=read.MustNamespace(relation.Namespace());if ns.Temporary!=(ids.SessionToken{}){continue}
  facts:=CompatibleStatistics(entry.Statistics,relation);if facts==nil{continue}
  for key,fact:=range facts.Columns{
   if err:=ctx.Err();err!=nil{return nil,err};slot:=relation.IndexForAttribute(key.Attribute);if slot<0{continue}
   if fact.Hierarchy!=nil&&!StatisticsHierarchyCurrent(fact.Hierarchy,relation,read){continue}
   r:=AttributeStatisticsSnapshot{Schema:ns.Name(),Relation:relation.Name(),Column:relation.Columns[slot].Name,Attribute:key.Attribute,Inherited:key.Inherited,Usage:ndistinctUsageRecord(read,fact.Usage),Rows:fact.Rows,NullCount:fact.NullCount,DistinctCount:fact.DistinctCount,DistinctKnown:fact.DistinctKnown,DistinctOverride:math.Float64bits(fact.DistinctOverride),AverageWidth:fact.AverageWidth,MCV:make([]StatisticsMCVSnapshot,len(fact.MCV)),Histogram:make([]json.RawMessage,len(fact.Histogram)),Slots:[]AttributeStatisticsSlotSnapshot{}}
   r.Hierarchy=fact.Hierarchy!=nil
   for i,mcv:=range fact.MCV{raw,err:=codec.Encode(fact.Usage,mcv.Value);if err!=nil{return nil,err};r.MCV[i]=StatisticsMCVSnapshot{Value:raw,Count:mcv.Count}}
   for i,v:=range fact.Histogram{raw,err:=codec.Encode(fact.Usage,v);if err!=nil{return nil,err};r.Histogram[i]=raw}
   if fact.Imported!=nil{p:=fact.Imported;r.Imported=true;r.NullFractionSet,r.DistinctSet=p.NullFractionSet,p.DistinctSet;r.NullFraction,r.Distinct=math.Float32bits(p.NullFraction),math.Float32bits(p.Distinct)
    for _,slot:=range p.Slots{if slot.Kind==0{continue};values,err:=encode(slot.Values);if err!=nil{return nil,err};numbers,err:=encode(slot.Numbers);if err!=nil{return nil,err};r.Slots=append(r.Slots,AttributeStatisticsSlotSnapshot{Kind:slot.Kind,Values:values,Numbers:numbers})}
   }
   records=append(records,r)
  }
 }
 slices.SortFunc(records,func(a,b AttributeStatisticsSnapshot)int{if n:=strings.Compare(a.Schema,b.Schema);n!=0{return n};if n:=strings.Compare(a.Relation,b.Relation);n!=0{return n};if a.Attribute<b.Attribute{return -1};if a.Attribute>b.Attribute{return 1};if a.Inherited==b.Inherited{return 0};if a.Inherited{return 1};return -1})
 return records,ctx.Err()
}
func RestoreAttributeStatisticsSnapshots(ctx context.Context,read catalog.Read,records []AttributeStatisticsSnapshot,codec StatisticsSnapshotCodec)(map[ids.ObjectAddress]*RelationMetadata,error){
 metadata:=make(map[ids.ObjectAddress]*RelationMetadata)
 type identity struct{address ids.ObjectAddress;key StatisticsKey};seen:=make(map[identity]bool)
 decode:=func(r *AttributeStatisticsValueSnapshot)(sqltype.Value,error){
  if r==nil{return sqltype.Value{},nil};u,err:=attributeSnapshotUsage(read,r.Usage);if err!=nil{return sqltype.Value{},err}
  elements:=make([]sqltype.Value,len(r.Elements));for i,raw:=range r.Elements{v,err:=codec.Decode(u,raw);if err!=nil{return sqltype.Value{},err};elements[i]=v}
  if r.Statistic{d,_:=read.LookupType(2277);return sqltype.StatisticArrayValue(d,u,r.Dimensions,elements)}
  d,ok:=read.LookupType(u.Type.AssociatedArray());if !ok{return sqltype.Value{},fmt.Errorf("stuntgres: missing statistics numbers array")};return sqltype.ArrayValue(d,r.Dimensions,elements)
 }
 for _,r:=range records{
  if err:=ctx.Err();err!=nil{return nil,err};if err:=r.Validate();err!=nil{return nil,err}
  ns:=read.NameAddress(ids.NameKey{ClassID:ids.NamespaceClassID,Name:r.Schema});relation,ok:=read.Relation(read.NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:ns,Name:r.Relation}));if !ok{return nil,fmt.Errorf("stuntgres: missing attribute statistics relation")}
  slot:=relation.IndexForAttribute(r.Attribute);if slot<0||relation.Columns[slot].Name!=r.Column||ndistinctUsageRecord(read,relation.Columns[slot].Usage)!=r.Usage{return nil,fmt.Errorf("stuntgres: incompatible attribute statistics column")}
  key:=StatisticsKey{Attribute:r.Attribute,Inherited:r.Inherited};id:=identity{relation.Address(),key};if seen[id]{return nil,fmt.Errorf("stuntgres: duplicate attribute statistics snapshot")};seen[id]=true
  fact:=&ColumnStatistics{Usage:relation.Columns[slot].Usage,Rows:r.Rows,NullCount:r.NullCount,DistinctCount:r.DistinctCount,DistinctKnown:r.DistinctKnown,DistinctOverride:math.Float64frombits(r.DistinctOverride),AverageWidth:r.AverageWidth,MCV:make([]StatisticMCV,len(r.MCV)),Histogram:make([]sqltype.Value,len(r.Histogram))}
  if r.Hierarchy{if !r.Inherited{return nil,fmt.Errorf("stuntgres: local statistics hierarchy")};var err error;fact.Hierarchy,err=restoredNDistinctHierarchy(read,relation);if err!=nil{return nil,err}}
  for i,mcv:=range r.MCV{v,err:=codec.Decode(fact.Usage,mcv.Value);if err!=nil{return nil,err};if v.IsNull(){return nil,fmt.Errorf("stuntgres: null measured MCV")};fact.MCV[i]=StatisticMCV{Value:v,Count:mcv.Count}}
  for i,raw:=range r.Histogram{v,err:=codec.Decode(fact.Usage,raw);if err!=nil{return nil,err};if v.IsNull(){return nil,fmt.Errorf("stuntgres: null measured histogram")};fact.Histogram[i]=v}
  if r.Imported{fact.Imported=&ImportedStatistics{NullFractionSet:r.NullFractionSet,DistinctSet:r.DistinctSet,NullFraction:math.Float32frombits(r.NullFraction),Distinct:math.Float32frombits(r.Distinct)}
   for _,slot:=range r.Slots{v,err:=decode(slot.Values);if err!=nil{return nil,err};n,err:=decode(slot.Numbers);if err!=nil{return nil,err}
    if err=validateAttributeSnapshotSlot(read,fact,slot.Kind,v,n);err!=nil{return nil,err}
    if err=fact.Imported.SetSlot(AttributeStatisticsSlot{Kind:slot.Kind,Values:v,Numbers:n});err!=nil{return nil,err}
   }
  }
  entry:=metadata[relation.Address()];if entry==nil{entry=&RelationMetadata{};metadata[relation.Address()]=entry};entry.Statistics=MergeStatistics(entry.Statistics,map[StatisticsKey]*ColumnStatistics{key:fact})
 }
 return metadata,nil
}
func attributeSnapshotUsage(read catalog.Read,r NDistinctUsageRecord)(sqltype.Usage,error){
 ns:=read.NameAddress(ids.NameKey{ClassID:ids.NamespaceClassID,Name:r.TypeSchema});d,ok:=read.Type(read.NameAddress(ids.NameKey{ClassID:ids.TypeClassID,Namespace:ns,Name:r.TypeName}));if !ok{return sqltype.Usage{},fmt.Errorf("stuntgres: missing statistics snapshot type")};u:=d.SQLType.Usage();u.Typmod=r.Typmod
 if r.CollationName!=""{ns=read.NameAddress(ids.NameKey{ClassID:ids.NamespaceClassID,Name:r.CollationSchema});d,ok:=read.Collation(read.NameAddress(ids.NameKey{ClassID:ids.CollationClassID,Namespace:ns,Name:r.CollationName}));if !ok{return sqltype.Usage{},fmt.Errorf("stuntgres: missing statistics snapshot collation")};var err error;u.Collation,err=d.Usage();if err!=nil{return sqltype.Usage{},err}}
 if ndistinctUsageRecord(read,u)!=r{return sqltype.Usage{},fmt.Errorf("stuntgres: incompatible statistics snapshot usage")};return u,nil
}
func validateAttributeSnapshotSlot(read catalog.Read,fact *ColumnStatistics,kind int16,v,n sqltype.Value)error{
 invalid:=func()error{return fmt.Errorf("stuntgres: incompatible attribute statistics kind %d",kind)}
 element:=fact.Usage.Type
 switch kind{
 case 4,5:
  var available bool
  element,available=AttributeStatisticsElementType(read,element)
  if !available{return invalid()}
 case 6:
  if !element.IsRange()&&!element.IsMultirange(){return invalid()}
  element,_=read.LookupType(sqltype.Float8)
 case 7:
  if element.IsMultirange(){element=element.RangeType()}
  if !element.IsRange(){return invalid()}
 }
 if v.Type()!=nil{
  usage,ok:=v.StatisticArrayUsage();if !ok||v.ArrayRank()!=1{return invalid()}
  for i:=range v.ArrayLen(){if v.ArrayElement(i).IsNull(){return invalid()}}
  expected:=element.Usage();expected.Typmod=fact.Usage.Typmod
  if element==fact.Usage.Type{expected=fact.Usage}
  if usage!=expected{return invalid()}
 }
 if n.Type()!=nil{if n.IsNull()||n.Type().ID()!=1021||n.ArrayRank()!=1{return invalid()};for i:=range n.ArrayLen(){if n.ArrayElement(i).IsNull(){return invalid()}};if (kind==3||kind==6)&&n.ArrayLen()!=1{return invalid()}}
 if kind==1&&v.ArrayLen()!=n.ArrayLen(){return invalid()}
 if kind==2||kind==3{available,err:=AttributeStatisticsHasLessThan(read,fact.Usage);if err!=nil{return err};if !available{return invalid()}}
 return nil
}
func EncodeAttributeStatisticsSnapshot(r AttributeStatisticsSnapshot)(string,error){if err:=r.Validate();err!=nil{return "",err};data,err:=json.Marshal(r);if err!=nil{return "",err};return "-- "+AttributeStatisticsSnapshotMarker+" "+base64.StdEncoding.EncodeToString(data)+"\n",nil}
func DecodeAttributeStatisticsSnapshot(comment string)(AttributeStatisticsSnapshot,bool,error){
 var r AttributeStatisticsSnapshot;text:=strings.TrimSpace(comment);if !strings.HasPrefix(text,"stuntgres-attribute-statistics-"){return r,false,nil}
 invalid:=func()(AttributeStatisticsSnapshot,bool,error){return r,true,fmt.Errorf("stuntgres: invalid attribute statistics snapshot")}
 marker,payload,ok:=strings.Cut(text," ");if !ok||marker!=AttributeStatisticsSnapshotMarker{return invalid()};data,err:=base64.StdEncoding.Strict().DecodeString(payload);if err!=nil{return invalid()}
 if err=validateSnapshotJSON(json.NewDecoder(bytes.NewReader(data)));err!=nil{return invalid()};decoder:=json.NewDecoder(bytes.NewReader(data));decoder.DisallowUnknownFields();if err=decoder.Decode(&r);err!=nil{return invalid()};if err=decoder.Decode(new(any));err!=io.EOF{return invalid()};if err=r.Validate();err!=nil{return invalid()};return r,true,nil
}
