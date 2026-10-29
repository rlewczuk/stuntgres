package reference_test

import (
 "database/sql"
 "fmt"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferencePGPreparedStatementsDiagnostics(t *testing.T) {
 for _,prepared:=range []bool{false,true} {
  t.Run(fmt.Sprintf("prepared=%t",prepared),func(t *testing.T) {
   sqlPreparedScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
    source:="PREPARE pps_error(integer,text) AS SELECT $1 AS i,$2 AS s"
    sqlPreparedStep(t,p,b,c,fixture.Exec(source,0));created:=ppsCreated(t,p,c,"pps_error")
    count:=sqlPreparedDiagnostic("42601",`wrong number of parameters for prepared statement "pps_error"`);count.Detail="Expected 2 parameters but got 1."
    booleanQuery:="EXECUTE pps_error(TRUE,'x')"
    boolean:=sqlPreparedDiagnostic("42804","parameter $1 of type boolean cannot be coerced to the expected type integer");boolean.Hint="You will need to rewrite or cast the expression.";boolean.Position=sqlPreparedPosition(booleanQuery,"TRUE")
    sqlPreparedStep(t,p,b,c,fixture.Exec("CREATE TABLE pps_conflict(v integer)",0))
    conflictQuery:="PREPARE pps_bad AS SELECT $1 FROM pps_conflict WHERE v=$1"
    conflict:=sqlPreparedDiagnostic("42P08","inconsistent types deduced for parameter $1");conflict.Detail="integer versus text";conflict.Position=sqlPreparedPosition(conflictQuery,"$1")
    for _,tc:=range []struct{query string;diagnostic fixture.Diagnostic}{
     {"PREPARE pps_error AS SELECT 99",sqlPreparedDiagnostic("42P05",`prepared statement "pps_error" already exists`)},
     {"EXECUTE pps_missing",sqlPreparedMissing("pps_missing")},
     {"DEALLOCATE pps_missing",sqlPreparedMissing("pps_missing")},
     {"EXECUTE pps_error(1)",count},{booleanQuery,boolean},
     {"PREPARE pps_unresolved(unknown) AS SELECT 1",sqlPreparedDiagnostic("42P18","could not determine data type of parameter $1")},
     {conflictQuery,conflict},
    } {
     sqlPreparedStep(t,p,b,c,sqlPreparedError(tc.query,tc.diagnostic,prepared))
     ppsSignature(t,p,b,c,"pps_error",source,"{integer,text}","{integer,text}",true)
     if !ppsCreated(t,p,c,"pps_error").Equal(created) {t.Fatal("failure changed old creation time")}
     ppsAbsent(t,p,b,c,"pps_missing");ppsAbsent(t,p,b,c,"pps_unresolved");ppsAbsent(t,p,b,c,"pps_bad")
    }
    for _,tc:=range []struct{query,verb,action,command string}{
     {"INSERT INTO pg_prepared_statements(name) VALUES('forbidden')","insert into","inserting into","INSERT"},
     {"UPDATE pg_prepared_statements SET name='forbidden'","update","updating","UPDATE"},
     {"DELETE FROM pg_prepared_statements","delete from","deleting from","DELETE"},
    } {
     d:=sqlPreparedDiagnostic("55000",fmt.Sprintf("cannot %s view %q",tc.verb,"pg_prepared_statements"))
     d.Detail="Views that do not select from a single table or view are not automatically updatable."
     d.Hint=fmt.Sprintf("To enable %s the view, provide an INSTEAD OF %s trigger or an unconditional ON %s DO INSTEAD rule.",tc.action,tc.command,tc.command)
     step:=fixture.ErrorDiagnostic(tc.query,d);step.Prepared=prepared
     sqlPreparedStep(t,p,b,c,step)
     ppsSignature(t,p,b,c,"pps_error",source,"{integer,text}","{integer,text}",true)
     if !ppsCreated(t,p,c,"pps_error").Equal(created) {t.Fatal("view mutation changed definition")}
    }
   })
  })
 }
}
