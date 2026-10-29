package enginetests_test

import (
 "context"
 "strings"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/storage"
 "stuntgres/internal/engine/table"
 "stuntgres/internal/engine/typedef"
 "stuntgres/internal/sqltype"
)

func TestModdatetimeDumpPrecision(t *testing.T) {
 for _,typ:=range []string{"timestamp(0)","timestamp(3)","timestamptz(0)","timestamptz(3)","timestamp","timestamp(6)"} {t.Run(typ,func(t *testing.T){
  e:=engine.New();op,done:=enginetest.RoleOperation(t,e,"postgres");defer done();enginetest.RoleExecute(t,e,op,"CREATE TABLE mdt(stamp "+typ+")")
  v:=e.CatalogRead();ns:=v.NameAddress(ids.NameKey{ClassID:ids.NamespaceClassID,Name:"public"});r:=v.MustRelation(v.NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:ns,Name:"mdt"}));d:=r.Columns[0].Usage.Type
  for _,text:=range []string{"2001-02-03 04:05:06.123456","2001-02-03 04:05:06","infinity","NULL"} {
   input:=text;if d.ID()==sqltype.Timestamptz&&text!="infinity"&&text!="NULL" {input+="+00"}
   value:=sqltype.Null(d);var err error;if text!="NULL" {value,err=d.Input(context.Background(),input,-1);if err!=nil {t.Fatal(err)}}
   captured:=table.DumpRelation{Schema:"public",Name:"mdt",Descriptor:r,Columns:r.Columns,Attributes:r.Attributes,Frontier:1,Versions:[]storage.RowVersion{{Row:1,Version:1,Born:1,Values:[]sqltype.Value{value}}}}
   var out strings.Builder;if err:=captured.EmitRows(context.Background(),v,func(s string)error{out.WriteString(s);return nil});err!=nil {t.Fatal(err)}
   lines:=strings.Split(out.String(),"\n");var record typedef.DomainReplayRecord;found:=false
   for _,line:=range lines {if strings.HasPrefix(line,"-- ") {candidate,yes,err:=typedef.DecodeDomainReplay(strings.TrimPrefix(line,"-- "));if err!=nil {t.Fatal(err)};if yes {record=candidate;found=true}}}
   want:=strings.Contains(typ,"(")&&!strings.Contains(typ,"(6)")&&text=="2001-02-03 04:05:06.123456"
   if found!=want {t.Fatal("precision provenance",typ,text,out.String())}
   if want {if len(record.Cells)!=1||record.Cells[0].Value.Kind!="timestamp_exact"||record.Cells[0].Value.Scalar==nil||*record.Cells[0].Value.Scalar!=input {t.Fatal("canonical exact temporal record",record)};if !strings.Contains(out.String(),"'"+input+"'") {t.Fatal("plain canonical placeholder missing",out.String())}}
  }
 }) }
}
