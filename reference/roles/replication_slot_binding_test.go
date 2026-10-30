package reference_test

import (
 "database/sql"
 "fmt"
 "strconv"
 "strings"
 "testing"
 "unicode/utf8"
 "stuntgres/reference/fixture"
)
func TestReferenceReplicationSlotSignatures(t *testing.T) {
 slotCases(t,func(s *fixture.RoleMembershipScenario,_ *sql.DB){
  for _,sig:=range slotSignatures {
   predefinedSelect(s,"SELECT '"+sig.name+"("+sig.types+")'::regprocedure::oid::bigint AS id",[]string{"id"},[]string{"INT8"},[][]any{{sig.oid}})
   result:=int64(2249); if sig.oid==3780 { result=2278 }
   q:="SELECT proisstrict,provolatile,proparallel,proretset,prosecdef,pronargs,pronargdefaults,provariadic::bigint,prorettype::bigint,prolang::bigint FROM pg_proc WHERE oid="+strconv.FormatInt(sig.oid,10)
   predefinedSelect(s,q,[]string{"proisstrict","provolatile","proparallel","proretset","prosecdef","pronargs","pronargdefaults","provariadic","prorettype","prolang"},[]string{"BOOL","CHAR","CHAR","BOOL","BOOL","INT2","INT2","INT8","INT8","INT8"},[][]any{{true,"v","u",false,false,int64(len(sig.args)),int64(sig.defaults),int64(0),result,int64(12)}})
   ids:=strings.Split(sig.types,","); oidText:=make([]string,len(ids)); for i,id:=range ids { switch id { case "name":oidText[i]="19"; case "boolean":oidText[i]="16"; case "pg_lsn":oidText[i]="3220" } }
   var names,modes,all,defaults any
   if sig.oid!=3780 { second:="lsn"; if sig.oid==3878 { second="end_lsn" }; names="{"+sig.labels+",slot_name,"+second+"}"; modes="{"+strings.Repeat("i,",len(ids))+"o,o}"; all="{"+strings.Join(oidText,",")+",19,3220}" }
   if sig.defaults>0 { defaults=strings.TrimSuffix(strings.Repeat("false, ",sig.defaults),", ") }
   predefinedSelect(s,"SELECT proargtypes::text,proargnames::text,proargmodes::text,proallargtypes::text,pg_get_expr(proargdefaults,0) AS defaults FROM pg_proc WHERE oid="+strconv.FormatInt(sig.oid,10),[]string{"proargtypes","proargnames","proargmodes","proallargtypes","defaults"},[]string{"TEXT","TEXT","TEXT","TEXT","TEXT"},[][]any{{strings.Join(oidText," "),names,modes,all,defaults}})
   for count:=len(sig.args)-sig.defaults; count<=len(sig.args); count++ { args:=append([]string(nil),sig.args[:count]...); args[0]="NULL"; predefinedSelect(s,"SELECT "+slotCall(sig,args),[]string{sig.name},[]string{map[bool]string{true:"VOID",false:"RECORD"}[sig.oid==3780]},[][]any{{nil}}) }
   if sig.labels!="" { labels:=strings.Split(sig.labels,","); args:=make([]string,len(sig.args)); for i:=range args { j:=len(args)-1-i; value:=sig.args[j]; if j==0 { value="NULL" }; args[i]=labels[j]+" => "+value }; if sig.oid!=3780 { c,ty:=slotFields(sig); predefinedSelect(s,"SELECT * FROM "+slotCall(sig,args),c,ty,[][]any{{nil,nil}}) } }
  }
 })
}
func TestReferenceReplicationSlotMetadata(t *testing.T) {
 slotCases(t,func(s *fixture.RoleMembershipScenario,_ *sql.DB){
  for _,sig:=range slotSignatures {
   typ:="RECORD"; if sig.oid==3780 { typ="VOID" }
   predefinedSelect(s,"SELECT "+slotCall(sig,sig.args)+" LIMIT 0",[]string{sig.name},[]string{typ},nil)
   if sig.oid==3780 { continue }
   c,ty:=slotFields(sig)
   predefinedSelect(s,"SELECT * FROM pg_catalog."+slotCall(sig,sig.args)+" LIMIT 0",c,ty,nil)
   args:=append([]string(nil),sig.args...); args[0]="NULL"
   predefinedSelect(s,"SELECT * FROM "+slotCall(sig,args),c,ty,[][]any{{nil,nil}})
   predefinedSelect(s,"SELECT * FROM "+slotCall(sig,args)+" AS r(a,b)",[]string{"a","b"},ty,[][]any{{nil,nil}})
   args[0]="v.slot"
   predefinedSelect(s,"SELECT r.* FROM (VALUES(NULL::name)) AS v(slot) CROSS JOIN LATERAL "+slotCall(sig,args)+" AS r",c,ty,[][]any{{nil,nil}})
  }
 })
}
func TestReferenceReplicationSlotNulls(t *testing.T) {
 slotCases(t,func(s *fixture.RoleMembershipScenario,_ *sql.DB){
  s.Exec("SET ROLE rsl_actor")
  for _,sig:=range slotSignatures { for pos:=range sig.args { args:=append([]string(nil),sig.args...); args[pos]="NULL"; typ:="RECORD"; if sig.oid==3780 { typ="VOID" }; predefinedSelect(s,"SELECT "+slotCall(sig,args),[]string{sig.name},[]string{typ},[][]any{{nil}}); if sig.oid!=3780 { c,ty:=slotFields(sig); predefinedSelect(s,"SELECT * FROM "+slotCall(sig,args),c,ty,[][]any{{nil,nil}}) } } }
  s.Exec("RESET ROLE")
  for _,tc:=range []struct{q,value,message string}{
   {"SELECT * FROM pg_create_physical_replication_slot(NULL,$1::boolean)","bad_bool",`invalid input syntax for type boolean: "bad_bool"`},
   {"SELECT * FROM pg_replication_slot_advance(NULL,$1::pg_lsn)","bad_lsn",`invalid input syntax for type pg_lsn: "bad_lsn"`},
  } { h:=predefinedHeld(s,tc.q); d:=fixture.Diagnostic{Code:"22P02",Message:tc.message,Context:"unnamed portal parameter $1 = '...'"}; logicalDecodingHeldFailure(s,h,d,tc.value) }
 })
}
func TestReferenceReplicationSlotBindingDiagnostics(t *testing.T) {
 slotCases(t,func(s *fixture.RoleMembershipScenario,_ *sql.DB){
  for _,sig:=range slotSignatures {
   maxArgs:=len(sig.args); if sig.name=="pg_copy_physical_replication_slot" { maxArgs=3 }; if sig.name=="pg_copy_logical_replication_slot" { maxArgs=4 }
   for _,tc:=range []struct{args,types string}{{"",""},{"1::integer"+strings.Repeat(",NULL",len(sig.args)-1),"integer"+strings.Repeat(", unknown",len(sig.args)-1)},{strings.TrimSuffix(strings.Repeat("NULL,",maxArgs+1),","),strings.TrimSuffix(strings.Repeat("unknown, ",maxArgs+1),", ")}} {
    q:="SELECT * FROM "+sig.name+"("+tc.args+")"; predefinedFailure(s,q,fixture.Diagnostic{Code:"42883",Message:"function "+sig.name+"("+tc.types+") does not exist",Hint:fileListSignatureHint,Position:"15"},true,"prepare")
   }
  }
  for _,tc:=range []struct{name,args,types string}{
   {"pg_create_logical_replication_slot","'x'","unknown"},
   {"pg_copy_physical_replication_slot","'x'","unknown"},
   {"pg_copy_logical_replication_slot","'x'","unknown"},
   {"pg_replication_slot_advance","'x'","unknown"},
   {"pg_create_logical_replication_slot","'x','pgoutput',two_phase => true","unknown, unknown, two_phase => boolean"},
   {"pg_drop_replication_slot","slot_name => 'x'","slot_name => unknown"},
   {"pg_copy_logical_replication_slot","'x','y',plugin => 'pgoutput'","unknown, unknown, plugin => unknown"},
   {"pg_replication_slot_advance","'x',true","unknown, boolean"},
   {"pg_create_physical_replication_slot","'x',1::integer","unknown, integer"},
  } { q:="SELECT * FROM "+tc.name+"("+tc.args+")"; predefinedFailure(s,q,fixture.Diagnostic{Code:"42883",Message:"function "+tc.name+"("+tc.types+") does not exist",Hint:fileListSignatureHint,Position:"15"},true,"prepare") }
  for _,prefix:=range []string{"SELECT * FROM ","SELECT /* λ雪 */ * FROM "} { for _,tc:=range []struct{call,token,typ string}{{"pg_create_physical_replication_slot(NULL,'bad_bool')","'bad_bool'","boolean"},{"pg_replication_slot_advance(NULL,'bad_lsn')","'bad_lsn'","pg_lsn"}} { q:=prefix+tc.call; token:=strings.Trim(tc.token,"'"); predefinedFailure(s,q,fixture.Diagnostic{Code:"22P02",Message:fmt.Sprintf("invalid input syntax for type %s: %q",tc.typ,token),Position:fmt.Sprint(utf8.RuneCountInString(q[:strings.Index(q,tc.token)])+1)},true,"prepare") } }
  for _,tc:=range []struct{args,token,message string}{{"slot_name=>'x',slot_name=>'y'","slot_name=>'y'",`argument name "slot_name" used more than once`},{"slot_name=>'x',true","true","positional argument cannot follow named argument"}} { q:="SELECT * FROM pg_create_physical_replication_slot("+tc.args+")"; predefinedFailure(s,q,fixture.Diagnostic{Code:"42601",Message:tc.message,Position:fmt.Sprint(strings.Index(q,tc.token)+1)},true,"prepare") }
 })
}
