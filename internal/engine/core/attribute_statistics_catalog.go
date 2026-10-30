package core

import (
 "context"
 "fmt"
 "stuntgres/internal/engine/access"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/engine/storage"
 "stuntgres/internal/sqltype"
)

// VisitAttributeStatistics shares the catalog capture frontier with pg_class.
// Own uncommitted selected deltas are resolved before privilege-filtered output.
func(r EngineCatalogRuntime) VisitAttributeStatistics(ctx context.Context,source *catalog.Relation,visit func([]sqltype.Value)error)error{
 for _,raw:=range r.Auth.Catalog.Descriptors(){
  relation,ok:=raw.(*catalog.Relation);if !ok{continue}
  active,err:=access.RowSecurityApplies(r.Auth.Roles,relation,r.Auth.State.CurrentUser);if err!=nil{return err};if active{continue}
  metadata:=session.ResolveRelationMetadata(r.Auth.PendingCatalog(),r.engine.Storage.RelationMetadata,relation.Address())
  facts:=session.ResolveStatistics(r.Auth.PendingCatalog(),relation.Address(),metadata,relation,r.Auth.Catalog);if facts==nil{continue}
  ns,ok:=r.Auth.Catalog.Namespace(relation.Namespace());if !ok{return fmt.Errorf("stuntgres: statistics namespace unavailable")}
  for key,fact:=range facts.Columns{
   if err:=ctx.Err();err!=nil{return err}
   slot:=-1;for i,attribute:=range relation.Attributes{if attribute==key.Attribute{slot=i;break}};if slot<0{continue}
   allowed,err:=r.Auth.View.RelationPrivilege(ctx,r.Auth.State.CurrentUser.ID(),relation,key.Attribute,false,authorization.SelectPrivilege,0);if err!=nil{return err};if !allowed{continue}
   row:=make([]sqltype.Value,len(source.Columns));for i,column:=range source.Columns{row[i]=sqltype.ResultNull(column.Usage)}
   row[0]=sqltype.String(source.Columns[0].Usage.Type,ns.Name());row[1]=sqltype.String(source.Columns[1].Usage.Type,relation.Name());row[2]=sqltype.String(source.Columns[2].Usage.Type,relation.Columns[slot].Name)
   row[3]=sqltype.Boolean(source.Columns[3].Usage.Type,key.Inherited)
   row[4],_=sqltype.Float(source.Columns[4].Usage.Type,fact.NullFraction());row[5],_=sqltype.Integer(source.Columns[5].Usage.Type,int64(fact.AverageWidth));distinct,_:=fact.EncodedDistinct();row[6],_=sqltype.Float(source.Columns[6].Usage.Type,distinct)
   if err:=attributeStatsRowKinds(r.Auth.Catalog,source,fact,row);err!=nil{return err}
   if err:=visit(row);err!=nil{return err}
  }
 }
 return ctx.Err()
}
func attributeStatsRowKinds(_ catalog.Read,source *catalog.Relation,fact *storage.ColumnStatistics,row []sqltype.Value)error{
 for _,item:=range []struct{kind int16;values,numbers int}{{1,7,8},{2,9,-1},{3,-1,10},{4,11,12},{5,-1,13},{6,14,15},{7,16,-1}}{
  if slot,ok:=fact.Imported.Slot(item.kind);ok{
   if item.values>=0&&slot.Values.Type()!=nil{row[item.values]=slot.Values}
   if item.numbers>=0&&slot.Numbers.Type()!=nil{if item.kind==3||item.kind==6{row[item.numbers]=slot.Numbers.ArrayElement(0)}else{row[item.numbers]=slot.Numbers}}
   continue
  }
  if item.kind==1&&len(fact.MCV)!=0{
   values:=make([]sqltype.Value,len(fact.MCV));numbers:=make([]sqltype.Value,len(fact.MCV));for i,mcv:=range fact.MCV{values[i]=mcv.Value;numbers[i],_=sqltype.Float(source.Columns[8].Usage.Type.Element(),float64(mcv.Count)/float64(fact.Rows))}
   dims:=[]sqltype.ArrayDimension{{Lower:1,Length:int32(len(values))}};var err error;row[7],err=sqltype.OwnedStatisticArrayValue(source.Columns[7].Usage.Type,fact.Usage,dims,values);if err!=nil{return err};row[8],err=sqltype.OwnedArrayValue(source.Columns[8].Usage.Type,dims,numbers);if err!=nil{return err}
  }
  if item.kind==2&&len(fact.Histogram)!=0{v,err:=sqltype.StatisticArrayValue(source.Columns[9].Usage.Type,fact.Usage,[]sqltype.ArrayDimension{{Lower:1,Length:int32(len(fact.Histogram))}},fact.Histogram);if err!=nil{return err};row[9]=v}
 }
 return nil
}
