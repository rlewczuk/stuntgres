package reference_test

import (
 "database/sql"
 "encoding/binary"
 "fmt"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferencePageinspectBrinDiagnostics(t *testing.T){
 brinCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  for _,q:=range []string{"CREATE TABLE pi_data(n integer)","CREATE INDEX pi_idx ON pi_data USING brin(n)","CREATE INDEX pi_btree ON pi_data(n)"}{run(fixture.Exec(q,0))}
  calls:=[]string{"SELECT brin_part.brin_page_type($1)","SELECT * FROM brin_part.brin_metapage_info($1)","SELECT * FROM brin_part.brin_revmap_data($1)","SELECT * FROM brin_part.brin_page_items($1,'pi_idx'::regclass)"}
  for _,q:=range calls{
   for _,n:=range []int{0,8191,8193}{s:=fixture.ImplicitError(q,fixture.Diagnostic{Code:"22023",Message:"invalid page size",Detail:fmt.Sprintf("Expected 8192 bytes, got %d.",n)},make([]byte,n));s.ErrorStage="execute";fixture.BRINBoth(t,p,b,c,s)}
   bad:=fixture.BRINImage(0xf091);binary.LittleEndian.PutUint16(bad[16:],8183)
   s:=fixture.ImplicitError(q,fixture.Diagnostic{Code:"22023",Message:"input page is not a valid BRIN page",Detail:"Expected special size 8, got 9."},bad);s.ErrorStage="execute";fixture.BRINBoth(t,p,b,c,s)
  }
  for _,tc:=range []struct{q,label string;required uint16}{
   {calls[1],"metapage",0xf091},{calls[2],"revmap",0xf092},{calls[3],"regular",0xf093},
  }{s:=fixture.ImplicitError(tc.q,fixture.Diagnostic{Code:"22023",Message:fmt.Sprintf("page is not a BRIN page of type %q",tc.label),Detail:fmt.Sprintf("Expected special type %08x, got 00001234.",tc.required)},fixture.BRINImage(0x1234));s.ErrorStage="execute";fixture.BRINBoth(t,p,b,c,s)}
  for _,tc:=range []struct{index,code,message string}{
   {"'pi_btree'::regclass","42809",`"pi_btree" is not a BRIN index`},
   {"'pi_data'::regclass","42809",`"pi_data" is not an index`},
   {"4294967000::regclass","XX000","could not open relation with OID 4294967000"},
  }{s:=fixture.ImplicitError("SELECT * FROM brin_part.brin_page_items($1,"+tc.index+")",fixture.Diagnostic{Code:tc.code,Message:tc.message},[]byte{});s.ErrorStage="execute";fixture.BRINBoth(t,p,b,c,s)}
  fixture.BRINBoth(t,p,b,c,fixture.Select(calls[0],[]string{"brin_page_type"},[]string{"TEXT"},[][]any{{"meta"}},fixture.BRINMeta()))
 })
}
func TestReferencePageinspectBrinPermissions(t *testing.T){
 fixture.RoleReferenceCleanup(t,[]string{"pi_reader"})
 brinCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  for _,q:=range []string{"CREATE ROLE pi_reader","GRANT USAGE ON SCHEMA brin_part TO pi_reader","CREATE TABLE pi_data(n integer)","CREATE INDEX pi_idx ON pi_data USING brin(n)"}{run(fixture.Exec(q,0))}
  q:="SELECT brin_part.brin_page_type($1)";st,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer st.Close();held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:q}
  run(fixture.Exec("SET SESSION AUTHORIZATION pi_reader",0));defer func(){if _,err:=c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION");err!=nil{t.Error(err)}}()
  for _,call:=range []string{q,"SELECT * FROM brin_part.brin_metapage_info($1)","SELECT * FROM brin_part.brin_revmap_data($1)","SELECT * FROM brin_part.brin_page_items($1,4294967000::regclass)"}{
   for _,page:=range [][]byte{fixture.BRINMeta(),{},make([]byte,8192)}{s:=fixture.ImplicitError(call,fixture.Diagnostic{Code:"42501",Message:"must be superuser to use raw page functions"},page);s.ErrorStage="execute";fixture.BRINBoth(t,p,b,c,s)}
  }
  s:=fixture.ImplicitError(q,fixture.Diagnostic{Code:"42501",Message:"must be superuser to use raw page functions"},fixture.BRINMeta());fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
  fixture.BRINBoth(t,p,b,c,fixture.Select(q,[]string{"brin_page_type"},[]string{"TEXT"},[][]any{{nil}},nil))
  fixture.BRINBoth(t,p,b,c,fixture.Select("SELECT * FROM brin_part.brin_revmap_data($1)",[]string{"pages"},[]string{"TID"},nil,nil))
  fixture.BRINBoth(t,p,b,c,fixture.Select("SELECT * FROM brin_part.brin_page_items($1,4294967000::regclass)",brinItemNames,brinItemTypes,nil,nil))
  fixture.BRINBoth(t,p,b,c,fixture.Select("SELECT * FROM brin_part.brin_page_items($1,NULL::regclass)",brinItemNames,brinItemTypes,nil,[]byte{}))
  run(fixture.Exec("RESET SESSION AUTHORIZATION",0));fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,[]string{"brin_page_type"},[]string{"TEXT"},[][]any{{"meta"}},fixture.BRINMeta()))
 })
}
