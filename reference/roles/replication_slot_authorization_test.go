package reference_test

import (
 "database/sql"
 "fmt"
 "testing"
 "stuntgres/reference/fixture"
)
func TestReferenceReplicationSlotAuthorization(t *testing.T) {
 slotCases(t,func(s *fixture.RoleMembershipScenario,db *sql.DB){
  admin,err:=db.Conn(s.P.Ctx); if err!=nil { s.T.Fatal(err) }; defer admin.Close()
  alter:=func(q string){ s.T.Helper(); if _,err:=admin.ExecContext(s.P.Ctx,q); err!=nil { s.T.Fatal(q,err) } }
  s.Exec("ALTER ROLE rsl_repl REPLICATION"); s.Exec("GRANT rsl_repl TO rsl_member WITH INHERIT TRUE")
  for i,sig:=range slotSignatures {
   args:=append([]string(nil),sig.args...); args[0]="$1::name"; q:="SELECT "+slotCall(sig,args); held:=predefinedHeld(s,q)
   s.Exec("SET ROLE rsl_actor")
   logicalDecodingHeldFailure(s,held,logicalDecodingDenied(),"BAD")
   typ:="RECORD"; if sig.oid==3780 { typ="VOID" }
   fixture.RunStep(s.T,s.P.Ctx,held,s.Record,fixture.Select(q,[]string{sig.name},[]string{typ},[][]any{{nil}},nil))
   alter("ALTER ROLE rsl_actor REPLICATION")
   d:=logicalDecodingMissing("BAD"); if i<2 { d=fixture.Diagnostic{Code:"42602",Message:`replication slot name "BAD" contains invalid character`,Hint:"Replication slot names may only contain lower case letters, numbers, and the underscore character."} }
   logicalDecodingHeldFailure(s,held,d,"BAD")
   alter("ALTER ROLE rsl_actor NOREPLICATION"); logicalDecodingHeldFailure(s,held,logicalDecodingDenied(),"BAD")
   s.Exec("RESET ROLE"); s.Exec("SET ROLE rsl_member"); logicalDecodingHeldFailure(s,held,logicalDecodingDenied(),"BAD"); s.Exec("RESET ROLE")
   s.Exec("SET ROLE rsl_repl"); logicalDecodingHeldFailure(s,held,d,"BAD"); s.Exec("RESET ROLE")
   prepared:=fmt.Sprintf("rsl_sql_%d",i); s.Exec("PREPARE "+prepared+"(name) AS "+q)
   s.Exec("SET ROLE rsl_actor"); slotFailure(s,"EXECUTE "+prepared+"('BAD')",logicalDecodingDenied()); s.Exec("RESET ROLE"); slotFailure(s,"EXECUTE "+prepared+"('BAD')",d); s.Exec("DEALLOCATE "+prepared)
   func(){ s.Exec("BEGIN"); defer func(){ if _,err:=s.C.ExecContext(s.P.Ctx,"ROLLBACK"); err!=nil { s.T.Error(err) } }(); s.Exec("REVOKE EXECUTE ON FUNCTION "+sig.name+"("+sig.types+") FROM PUBLIC"); s.Exec("SET ROLE rsl_repl"); logicalDecodingHeldFailure(s,held,fixture.Diagnostic{Code:"42501",Message:"permission denied for function "+sig.name},nil) }()
  }
  // A SECURITY DEFINER must use its effective owner, not the caller's
  // authenticated superuser or inherited replication membership.
  s.Exec("CREATE FUNCTION rsl_definer() RETURNS void LANGUAGE sql SECURITY DEFINER AS $$SELECT pg_drop_replication_slot('rsl_absent')$$")
  s.Exec("ALTER FUNCTION rsl_definer() OWNER TO rsl_repl")
  s.Exec("SET ROLE rsl_actor")
  d:=logicalDecodingMissing("rsl_absent"); d.Context=`SQL function "rsl_definer" statement 1`
  slotFailure(s,"SELECT rsl_definer()",d)
  alter("ALTER ROLE rsl_repl NOREPLICATION"); d=logicalDecodingDenied(); d.Context=`SQL function "rsl_definer" statement 1`; slotFailure(s,"SELECT rsl_definer()",d)
  s.Exec("RESET ROLE")
 })
}
func TestReferenceReplicationSlotPreparedLifetime(t *testing.T) {
 slotCases(t,func(s *fixture.RoleMembershipScenario,_ *sql.DB){
  q:="SELECT * FROM pg_create_physical_replication_slot($1::name,temporary=>true)"; held:=predefinedHeld(s,q)
  check:=func(){ fixture.RunStep(s.T,s.P.Ctx,held,s.Record,fixture.Select(q,[]string{"slot_name","lsn"},[]string{"NAME","PG_LSN"},[][]any{{nil,nil}},nil)) }
  check(); logicalDecodingHeldFailure(s,held,fixture.Diagnostic{Code:"42602",Message:`replication slot name "BAD" contains invalid character`,Hint:"Replication slot names may only contain lower case letters, numbers, and the underscore character."},"BAD"); check()
  rows,err:=held.Statement.QueryContext(s.P.Ctx,nil); if err!=nil { s.T.Fatal(err) }; types,err:=rows.ColumnTypes(); if err!=nil { s.T.Fatal(err) }; columns,err:=rows.Columns(); if err!=nil { s.T.Fatal(err) }; var a,b any; if !rows.Next() { s.T.Fatal("missing null row") }; if err:=rows.Scan(&a,&b); err!=nil { s.T.Fatal(err) }; if err:=rows.Close(); err!=nil { s.T.Fatal(err) }
  s.Exec("SET ROLE rsl_actor"); check(); logicalDecodingHeldFailure(s,held,logicalDecodingDenied(),"rsl_absent"); s.Exec("RESET ROLE"); check()
  if a!=nil || b!=nil || columns[0]!="slot_name" || columns[1]!="lsn" || types[0].DatabaseTypeName()!="NAME" || types[1].DatabaseTypeName()!="PG_LSN" { s.T.Fatal("retained result changed",a,b,columns,types) }
 })
}
