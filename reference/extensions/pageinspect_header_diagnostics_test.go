package reference_test

import (
 "context"
 "database/sql"
 "errors"
 "fmt"
 "math"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferencePageinspectHeaderNulls(t *testing.T) {headerCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 headerHeld(t,p,b,c,headerScalar,[]fixture.Step{
  fixture.Select(headerScalar,[]string{"page_header"},[]string{"RECORD"},[][]any{{nil}},nil),
  fixture.Select(headerScalar,[]string{"page_header"},[]string{"RECORD"},[][]any{{"(0/0,0,0,0,0,0,0,0,0)"}},make([]byte,8192)),
  headerError(headerScalar,"22023","invalid page size","Expected 8192 bytes, got 0.",[]byte{}),
  fixture.Select(headerScalar,[]string{"page_header"},[]string{"RECORD"},[][]any{{nil}},nil),
 })
 headerHeld(t,p,b,c,headerSource,[]fixture.Step{headerStep(headerSource,nil,make([]any,9)),headerStep(headerSource,make([]byte,8192),fixture.HeaderZero)})
 for _,q:=range []string{checksumScalar,checksumSource} {
  steps:=[]fixture.Step{checksumStep(q,nil,int64(0),nil),checksumStep(q,fixture.HeaderPattern(1),nil,nil),checksumStep(q,nil,nil,nil),checksumStep(q,[]byte{},nil,nil),checksumStep(q,nil,int64(-1),nil),checksumStep(q,fixture.HeaderPattern(1),int64(0),int64(1175))}
  raw:=fixture.HeaderPattern(255);raw[14],raw[15]=0,0;steps=append(steps,checksumStep(q,raw,int64(0),nil))
  headerHeld(t,p,b,c,q,steps)
 }
 for _,q:=range []string{"SELECT header_part.page_checksum($1,10/$2::bigint)","SELECT header_part.page_checksum(blkno=>10/$2::bigint,page=>$1)"} {
  headerHeld(t,p,b,c,q,[]fixture.Step{headerError(q,"22012","division by zero","",nil,int64(0)),checksumStep(q,nil,int64(1),nil),checksumStep(q,fixture.HeaderPattern(1),int64(1),int64(1169))})
 }
})}

func headerAuthority(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper();run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 run(fixture.Exec("CREATE ROLE header_reader",0));run(fixture.Exec("GRANT USAGE ON SCHEMA header_part TO header_reader",0))
 defer func(){if _,e:=c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION");e!=nil {t.Error(e)}}()
 for _,q:=range []string{headerScalar,headerSource,checksumScalar,checksumSource} {
  st,e:=c.PrepareContext(p.Ctx,q);if e!=nil {t.Fatal(e)}
  held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:q}
  run(fixture.Exec("SET SESSION AUTHORIZATION header_reader",0))
  for _,raw:=range [][]byte{fixture.HeaderPattern(1),{},make([]byte,8192)} {
   args:=[]any{raw};if q==checksumScalar||q==checksumSource {args=append(args,int64(-1))}
   s:=headerError(q,"42501","must be superuser to use raw page functions","",args...)
   headerBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
  }
  var null fixture.Step
  switch q {
  case headerScalar:null=fixture.Select(q,[]string{"page_header"},[]string{"RECORD"},[][]any{{nil}},nil)
  case headerSource:null=headerStep(q,nil,make([]any,9))
  default:null=checksumStep(q,nil,int64(-1),nil)
  }
  headerBoth(t,p,b,c,null);fixture.RunStep(t,p.Ctx,held,b.Recorder,null)
  if q==checksumScalar||q==checksumSource {s:=checksumStep(q,[]byte{},nil,nil);headerBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}
  run(fixture.Exec("RESET SESSION AUTHORIZATION",0));st.Close()
 }
 // A strict scalar call with literal NULL can disappear during planning,
 // before runtime EXECUTE checks. RECORD-returning calls are not simplified
 // by REL_18_6 evaluate_function. A stored NULL Var keeps both calls admitted.
 run(fixture.Exec("CREATE TABLE header_part.header_acl_inputs(raw bytea)",0))
 run(fixture.Exec("INSERT INTO header_part.header_acl_inputs VALUES(NULL)",1))
 run(fixture.Exec("GRANT SELECT ON header_part.header_acl_inputs TO header_reader",0))
 for _,tc:=range []struct{signature,query,stored,name string}{
  {"page_header(bytea)","SELECT header_part.page_header(NULL::bytea)","SELECT header_part.page_header(raw) FROM header_part.header_acl_inputs","page_header"},
  {"page_checksum(bytea,bigint)","SELECT header_part.page_checksum(NULL::bytea,0)","SELECT header_part.page_checksum(raw,0) FROM header_part.header_acl_inputs","page_checksum"},
 } {
  run(fixture.Exec("REVOKE EXECUTE ON FUNCTION header_part."+tc.signature+" FROM PUBLIC",0))
  run(fixture.Exec("SET SESSION AUTHORIZATION header_reader",0))
  if tc.name=="page_checksum" {
   headerBoth(t,p,b,c,fixture.Select(tc.query,[]string{"page_checksum"},[]string{"INT2"},[][]any{{nil}}))
  } else {
   headerBoth(t,p,b,c,headerError(tc.query,"42501","permission denied for function "+tc.name,""))
  }
  headerHeld(t,p,b,c,tc.stored,[]fixture.Step{headerError(tc.stored,"42501","permission denied for function "+tc.name,"")})
  run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
  run(fixture.Exec("GRANT EXECUTE ON FUNCTION header_part."+tc.signature+" TO PUBLIC",0))
 }
 run(fixture.Exec("REVOKE USAGE ON SCHEMA header_part FROM header_reader",0))
 run(fixture.Exec("SET SESSION AUTHORIZATION header_reader",0))
 q:="SELECT header_part.page_header(NULL::bytea)"
 s:=fixture.ImplicitError(q,fixture.Diagnostic{Code:"42501",Message:"permission denied for schema header_part",Position:fixture.Position(8)});s.SelectRows=true
 headerBoth(t,p,b,c,s)
 run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
}

func TestReferencePageinspectHeaderDiagnostics(t *testing.T) {
 fixture.RoleReferenceCleanup(t,[]string{"header_reader"})
 headerCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,q:=range []string{headerScalar,headerSource,checksumScalar,checksumSource} {
  steps:=[]fixture.Step{}
  for _,n:=range []int{0,3,8191,8193} {args:=[]any{make([]byte,n)};if q==checksumScalar||q==checksumSource {args=append(args,int64(0))};steps=append(steps,headerError(q,"22023","invalid page size",fmt.Sprintf("Expected 8192 bytes, got %d.",n),args...))}
  if q==checksumScalar||q==checksumSource {
   for _,block:=range []int64{-1,4294967295,math.MinInt64,math.MaxInt64} {for _,raw:=range [][]byte{{},make([]byte,8192),fixture.HeaderPattern(1)} {steps=append(steps,headerError(q,"22023","invalid block number","",raw,block))}}
   steps=append(steps,headerError(q,"22023","invalid page size","Expected 8192 bytes, got 3.",make([]byte,3),int64(4294967294)),checksumStep(q,fixture.HeaderPattern(1),int64(0),int64(1175)))
  } else if q==headerSource {steps=append(steps,headerStep(q,fixture.HeaderAsymmetric(),fixture.HeaderAsymmetricRow))} else {steps=append(steps,fixture.Select(q,[]string{"page_header"},[]string{"RECORD"},[][]any{{"(0/0,0,0,0,0,0,0,0,0)"}},make([]byte,8192)))}
  headerHeld(t,p,b,c,q,steps)
 }
 for _,tc:=range []struct{query,message string}{
  {"SELECT header_part.page_header(1)","function header_part.page_header(integer) does not exist"},
  {"SELECT header_part.page_header()","function header_part.page_header() does not exist"},
  {"SELECT header_part.page_checksum(NULL::bytea,true)","function header_part.page_checksum(bytea, boolean) does not exist"},
  {"SELECT header_part.page_checksum(page=>NULL::bytea,wrong=>0)","function header_part.page_checksum(page => bytea, wrong => integer) does not exist"},
 } {
  s:=fixture.ImplicitError(tc.query,fixture.Diagnostic{Code:"42883",Message:tc.message,Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(8)});s.SelectRows=true
  headerBoth(t,p,b,c,s)
 }
 q:="SELECT header_part.page_checksum(page=>NULL::bytea,page=>NULL::bytea)"
 s:=fixture.ImplicitError(q,fixture.Diagnostic{Code:"42601",Message:"argument name \"page\" used more than once",Position:fixture.Position(52)});s.SelectRows=true;headerBoth(t,p,b,c,s)
 // Force conversion at expression execution, not protocol parameter decoding.
 // Even a NULL page cannot suppress evaluation of its non-NULL other child.
 conversion:="SELECT header_part.page_checksum($1,$2::text::bigint)"
 headerHeld(t,p,b,c,conversion,[]fixture.Step{
  headerError(conversion,"22P02","invalid input syntax for type bigint: \"bad\"","",nil,"bad"),
  headerError(conversion,"22003","value \"9223372036854775808\" is out of range for type bigint","",nil,"9223372036854775808"),
  checksumStep(conversion,fixture.HeaderPattern(1),"0",int64(1175)),
 })
 headerAuthority(t,p,b,c)
 // A decoder failure in a later VALUES row cannot publish an earlier prefix.
 for _,q:=range []string{"CREATE TABLE header_sentinel(value smallint)","INSERT INTO header_sentinel VALUES(7)"} {n:=int64(0);if q=="INSERT INTO header_sentinel VALUES(7)" {n=1};fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,n))}
 q="INSERT INTO header_sentinel VALUES(header_part.page_checksum($1,0)),(header_part.page_checksum($2,0))"
 s=headerError(q,"22023","invalid page size","Expected 8192 bytes, got 3.",fixture.HeaderPattern(1),make([]byte,3));s.SelectRows=false;headerBoth(t,p,b,c,s)
 headerBoth(t,p,b,c,fixture.Select("SELECT value FROM header_sentinel ORDER BY value",[]string{"value"},[]string{"INT2"},[][]any{{int64(7)}}))
 if !b.PG {
  ctx,cancel:=context.WithCancel(p.Ctx);cancel()
  _,cancelErr:=c.ExecContext(ctx,"INSERT INTO header_sentinel VALUES(header_part.page_checksum($1,0))",fixture.HeaderPattern(1))
  if !errors.Is(cancelErr,context.Canceled) {t.Fatal("pre-canceled decoder mutation",cancelErr)}
  var state interface{SQLState() string};if errors.As(cancelErr,&state) {t.Fatal("context error invented SQLSTATE",cancelErr)}
  headerBoth(t,p,b,c,fixture.Select("SELECT value FROM header_sentinel ORDER BY value",[]string{"value"},[]string{"INT2"},[][]any{{int64(7)}}))
 }
 })
}
