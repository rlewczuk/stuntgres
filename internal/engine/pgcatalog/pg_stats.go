package pgcatalog

import (
 "fmt"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

const StatsOID ids.OID = 12053

func capturePGStats(in Input)([][]sqltype.Value,error){
 address:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:StatsOID}
 if in.Owner==nil||in.Catalog.Owner()!=in.Owner||!RegisteredSource(in.Catalog,in.Source)||in.Source.Address()!=address||in.Runtime==nil{return nil,fmt.Errorf("stuntgres: invalid pg_stats source")}
 if err:=in.Runtime.ValidateCapture(in.Catalog,in.Roles,in.Ref,in.Source,StatsOID);err!=nil{return nil,err}
 if err:=in.Runtime.SelectColumns(in.Ref,in.Source);err!=nil{return nil,err}
 var rows [][]sqltype.Value
 err:=in.Runtime.VisitAttributeStatistics(in.Context,in.Source,func(row []sqltype.Value)error{
  if len(row)!=len(in.Source.Columns){return fmt.Errorf("stuntgres: invalid pg_stats row")}
  for i,v:=range row{if !v.ValidResult(in.Source.Columns[i].Usage){return fmt.Errorf("stuntgres: invalid pg_stats column %d",i)}}
  rows=append(rows,row);return in.Context.Err()
 })
 return rows,err
}
