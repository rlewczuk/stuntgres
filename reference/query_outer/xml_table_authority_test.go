package reference_test

import (
 "context"
 "database/sql"
 "strings"
 "stuntgres/reference/fixture"
 "testing"
)

func TestReferenceXMLTableAuthorityAtomicityAndCancellation(t *testing.T) {
 t.Run("statement atomicity",func(t *testing.T){
  resultsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
   run(fixture.Exec(`CREATE TABLE xt_atomic(v int)`,0));run(fixture.Exec(`INSERT INTO xt_atomic VALUES(99)`,1))
   for _,tc:=range []struct{doc,declaration,code,message string}{
    {`<r><row><v>7</v></row><row><v>8</v><v>9</v></row></r>`,`v int`,"21000","more than one value returned by column XPath expression"},
    {`<r><row><v>7</v></row><row><v>bad</v></row></r>`,`v int`,"22P02",`invalid input syntax for type integer: "bad"`},
    {`<r><row><v>7</v></row><row/></r>`,`v int NOT NULL`,"22004",`null is not allowed in column "v"`},
   } {
    q:=`INSERT INTO xt_atomic SELECT v FROM XMLTABLE('/r/row' PASSING ($1::xml) COLUMNS `+tc.declaration+`)`
    for _,prepared:=range []bool{false,true} {
     step:=fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic(tc.code,tc.message),tc.doc);step.Prepared=prepared;step.ErrorStage="execute";run(step)
     run(fixture.Select(`SELECT v FROM xt_atomic`,[]string{"v"},[]string{"INT4"},[][]any{{int64(99)}}))
    }
   }
  })
 })
 t.Run("live source and expression authority",func(t *testing.T){
  fixture.RoleReferenceCleanup(t,[]string{"xt_reader"})
  resultsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
   defer c.ExecContext(p.Ctx,`RESET SESSION AUTHORIZATION`)
   for _,q:=range []string{
    `CREATE USER xt_reader`, `GRANT USAGE ON SCHEMA public TO xt_reader`,
    `CREATE TABLE xt_acl(doc xml,other int)`, `INSERT INTO xt_acl VALUES('<r><v>7</v></r>',1)`,
    `CREATE FUNCTION xt_namespace() RETURNS text LANGUAGE SQL VOLATILE AS 'SELECT ''urn:r'''`,
    `CREATE FUNCTION xt_default() RETURNS int LANGUAGE SQL VOLATILE AS 'SELECT 42'`,
    `CREATE DOMAIN xt_acl_domain AS int`,
    `REVOKE USAGE ON TYPE xt_acl_domain FROM PUBLIC`,
   } { count:=int64(0);if strings.HasPrefix(q,"INSERT") {count=1};run(fixture.Exec(q,count)) }
   q:=`SELECT x.v FROM xt_acl d,XMLTABLE(XMLNAMESPACES(xt_namespace() AS p), '/r' PASSING d.doc COLUMNS v int,unused int PATH 'missing' DEFAULT xt_default()) x`
   run(fixture.Exec(`SET SESSION AUTHORIZATION xt_reader`,0))
   // Domain USAGE governs schema-object creation, not existing query values.
   fixture.SQLJSONBoth(t,p,b,c,fixture.Select(`SELECT v::int AS v FROM XMLTABLE('/r' PASSING '<r><v>7</v></r>' COLUMNS v public.xt_acl_domain)`,[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
   fixture.SQLJSONDiagnostic(t,p,b,c,q,fixture.ControlsReferenceDiagnostic("42501","permission denied for table xt_acl"),false)
   run(fixture.Exec(`RESET SESSION AUTHORIZATION`,0));run(fixture.Exec(`GRANT SELECT ON xt_acl TO xt_reader`,0));run(fixture.Exec(`SET SESSION AUTHORIZATION xt_reader`,0))
   statement,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer statement.Close()
   held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:q}
   check:=fixture.Select(q,[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}})
   fixture.RunStep(t,p.Ctx,held,b.Recorder,check)
   for _,tc:=range []struct{revoke,grant,message string}{
    {`REVOKE SELECT ON xt_acl FROM xt_reader`,`GRANT SELECT ON xt_acl TO xt_reader`,`permission denied for table xt_acl`},
    {`REVOKE EXECUTE ON FUNCTION xt_namespace() FROM PUBLIC`,`GRANT EXECUTE ON FUNCTION xt_namespace() TO PUBLIC`,`permission denied for function xt_namespace`},
    {`REVOKE EXECUTE ON FUNCTION xt_default() FROM PUBLIC`,`GRANT EXECUTE ON FUNCTION xt_default() TO PUBLIC`,`permission denied for function xt_default`},
   } {
    run(fixture.Exec(`RESET SESSION AUTHORIZATION`,0));run(fixture.Exec(tc.revoke,0));run(fixture.Exec(`SET SESSION AUTHORIZATION xt_reader`,0))
    fixture.SQLJSONDiagnostic(t,p,b,c,q,fixture.ControlsReferenceDiagnostic("42501",tc.message),false)
    denied:=fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic("42501",tc.message));denied.SelectRows=true;denied.ErrorStage="execute";fixture.RunStep(t,p.Ctx,held,b.Recorder,denied)
    run(fixture.Exec(`RESET SESSION AUTHORIZATION`,0));run(fixture.Exec(tc.grant,0));run(fixture.Exec(`SET SESSION AUTHORIZATION xt_reader`,0));fixture.RunStep(t,p.Ctx,held,b.Recorder,check)
   }
   run(fixture.Exec(`RESET SESSION AUTHORIZATION`,0))
   run(fixture.Exec(`REVOKE SELECT ON xt_acl FROM xt_reader`,0));run(fixture.Exec(`GRANT SELECT(other) ON xt_acl TO xt_reader`,0));run(fixture.Exec(`SET SESSION AUTHORIZATION xt_reader`,0))
   fixture.SQLJSONDiagnostic(t,p,b,c,q,fixture.ControlsReferenceDiagnostic("42501","permission denied for table xt_acl"),false)
   run(fixture.Exec(`RESET SESSION AUTHORIZATION`,0))
  })
 })
 t.Run("active cancellation and held recovery",xmlTableActiveCancellation)
}

func xmlTableActiveCancellation(t *testing.T) {
 resultsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  run(fixture.Exec(`CREATE SEQUENCE xt_progress`,0))
  run(fixture.Exec(`CREATE TABLE xt_cancel_input(doc xml,repetitions int)`,0))
  run(fixture.Exec(`INSERT INTO xt_cancel_input VALUES('<r><v>7</v></r>',1)`,1))
  run(fixture.Exec(`CREATE TABLE xt_cancel_output(v int)`,0));run(fixture.Exec(`INSERT INTO xt_cancel_output VALUES(99)`,1))
  source:=` FROM xt_cancel_input d CROSS JOIN LATERAL generate_series(1,d.repetitions) g(n) CROSS JOIN XMLTABLE(XMLNAMESPACES(nextval('xt_progress')::text AS p), '/r/v' PASSING d.doc COLUMNS v int PATH '.') x`
  readSQL:=`SELECT v`+source+` ORDER BY v`
  writeSQL:=`INSERT INTO xt_cancel_output SELECT v`+source
  read,err:=c.PrepareContext(p.Ctx,readSQL);if err!=nil {t.Fatal(err)};defer read.Close()
  write,err:=c.PrepareContext(p.Ctx,writeSQL);if err!=nil {t.Fatal(err)};defer write.Close()
  if !b.PG {
   ctx,cancel:=context.WithCancel(p.Ctx);cancel()
   rows,err:=read.QueryContext(ctx);if rows!=nil {rows.Close();t.Fatal("precanceled query published rows")};fixture.LexicalOrdinary(t,err,context.Canceled)
   result,err:=write.ExecContext(ctx);if result!=nil {t.Fatal("precanceled write published result")};fixture.LexicalOrdinary(t,err,context.Canceled)
  }
  large:=`<r>`+strings.Repeat(`<v>7</v>`,1<<15)+`</r>`
  admitted:=func(ctx context.Context){
   if !b.PG {return}
   for {
    var called bool
    if err:=fixture.Admin.QueryRowContext(ctx,`SELECT is_called FROM public.xt_progress`).Scan(&called);err!=nil {t.Fatal(err)}
    if called {return};if ctx.Err()!=nil {t.Fatal(ctx.Err())}
   }
  }
  for _,writing:=range []bool{false,true} {
   run(fixture.Prepared(fixture.Exec(`UPDATE xt_cancel_input SET doc=$1::xml,repetitions=100`,1,large)))
   run(fixture.SQLJSONQueryOne(`SELECT setval('xt_progress',1,false) AS v`,"INT8",int64(1)))
   query:=readSQL;if writing {query=writeSQL}
   fixture.BinaryConversionActiveCancelAfter(t,p,b,c,query,"xml.(*xpathFrame).eval",admitted,func(ctx context.Context)fixture.RelationOutcome{
    if writing {result,err:=write.ExecContext(ctx);return fixture.RelationOutcome{Result:result,Err:err}}
    rows,err:=read.QueryContext(ctx)
    if !b.PG || rows==nil {return fixture.RelationOutcome{Rows:rows,Err:err}}
    for rows.Next(){t.Error("canceled sorted query published rows")};if err==nil {err=rows.Err()};closed:=rows.Close();if err==nil {err=closed};return fixture.RelationOutcome{Err:err}
   })
   run(fixture.Select(`SELECT v FROM xt_cancel_output`,[]string{"v"},[]string{"INT4"},[][]any{{int64(99)}}))
   run(fixture.Exec(`UPDATE xt_cancel_input SET doc='<r><v>7</v></r>',repetitions=1`,1))
   fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:read,SQL:readSQL},b.Recorder,fixture.Select(readSQL,[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
   fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:write,SQL:writeSQL},b.Recorder,fixture.Exec(writeSQL,1))
   run(fixture.Exec(`DELETE FROM xt_cancel_output WHERE v=7`,1))
  }
 })
}
