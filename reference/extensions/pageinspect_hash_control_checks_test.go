package reference_test

import (
 "database/sql"
 "encoding/base64"
 "encoding/binary"
 "encoding/json"
 "math"
 "testing"
 "stuntgres/reference/fixture"
)
func hashChanged(i int)([]byte,[][]any){
 switch i{
 case 0:return fixture.HashPage(1),[][]any{{"overflow"}}
 case 1:return fixture.HashPage(2),[][]any{{int64(0),int64(0),int64(8192),int64(8148),int64(4294967295),int64(17),int64(2147483648),int64(2),int64(65408)}}
 case 2:return fixture.HashPage(1,fixture.BTTuple(77,3,16,42,0,0,0,0,0,0,0)),[][]any{{int64(1),"(77,3)",int64(42)}}
 default:raw:=fixture.HashMeta();binary.LittleEndian.PutUint64(raw[32:],math.Float64bits(19.25));row:=append([]any(nil),fixture.HashMetaRows[0]...);row[2]=float64(19.25);return raw,[][]any{row}
 }
}
func hashStoredInputs(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,held []fixture.HeldStatement){
 t.Helper()
 // Each view retains its selected decoder, but reads the current supplied bytes.
 for i:=range fixture.HashMembers{
  column:="bucket";if i==3{column="meta"}
  changed,want:=hashChanged(i)
  for _,tc:=range []struct{raw []byte;rows [][]any}{{changed,want},{nil,fixture.HashNullRows(i)},{fixture.HashOrdinary(i),fixture.HashRows(i)}}{
   fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("UPDATE hash_images SET "+column+"=$1",1,tc.raw))
   s:=fixture.HashMetadata(hashViewStep(i,tc.rows),b.PG,true);fixture.BRINBoth(t,p,b,c,s);if held!=nil{fixture.RunStep(t,p.Ctx,held[i],b.Recorder,s)}
  }
 }
}
func hashViewAuthority(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,held []fixture.HeldStatement){
 t.Helper();run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 defer func(){if _,err:=c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION");err!=nil{t.Error(err)}}()
 run(fixture.Exec("SET SESSION AUTHORIZATION hash_reader",0))
 for _,h:=range held{bad:=fixture.ImplicitError(h.SQL,fixture.Diagnostic{Code:"42501",Message:"must be superuser to use raw page functions"});bad.SelectRows=true;bad.ErrorStage="execute";fixture.RunStep(t,p.Ctx,h,b.Recorder,bad)}
 run(fixture.Exec("RESET SESSION AUTHORIZATION",0));run(fixture.Exec("UPDATE hash_images SET bucket=NULL,meta=NULL",1));run(fixture.Exec("SET SESSION AUTHORIZATION hash_reader",0))
 for i,h:=range held{fixture.RunStep(t,p.Ctx,h,b.Recorder,fixture.HashMetadata(hashViewStep(i,fixture.HashNullRows(i)),b.PG,true))}
 run(fixture.Exec("RESET SESSION AUTHORIZATION",0));run(fixture.Exec("UPDATE hash_images SET bucket=$1,meta=$2",1,fixture.HashBucket(),fixture.HashMeta()))
 for i,h:=range held{fixture.RunStep(t,p.Ctx,h,b.Recorder,fixture.HashMetadata(hashViewStep(i,fixture.HashRows(i)),b.PG,true))}
}
func hashBadOwner(t *testing.T,dump string)string{
 t.Helper();start,end,payload:=fixture.CatalogExtensionOwnerDirective(t,dump,"pageinspect",true)
 var record map[string]json.RawMessage;if err:=json.Unmarshal(payload,&record);err!=nil{t.Fatal(err)}
 var members []map[string]json.RawMessage;if err:=json.Unmarshal(record["members"],&members);err!=nil{t.Fatal(err)}
 found:=false;for _,member:=range members{var name string;if err:=json.Unmarshal(member["name"],&name);err!=nil{t.Fatal(err)};if name=="meta"{member["owner"]=json.RawMessage(`"hash_missing_owner"`);found=true}}
 if !found{t.Fatal("missing moved hash member")};var err error;record["members"],err=json.Marshal(members);if err!=nil{t.Fatal(err)};payload,err=json.Marshal(record);if err!=nil{t.Fatal(err)}
 return dump[:start]+"-- stuntgres-extension-owners-v3 "+base64.StdEncoding.EncodeToString(payload)+dump[end:]
}
