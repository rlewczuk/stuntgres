package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const diagnosticsStaticLeaf = "PL/pgSQL function dg_static_leaf() line 1 at GET DIAGNOSTICS"

func diagnosticsStaticContext(t *testing.T) {
	for _, tc := range []struct{ name, statement, query string }{
		{"static_into_suffix", "SELECT dg_static_leaf() INTO s", "SELECT dg_static_leaf()"},
		{"static_into_interior", "SELECT dg_static_leaf() INTO s FROM (VALUES(1)) q(i)", "SELECT dg_static_leaf()"+strings.Repeat(" ",8)+"FROM (VALUES(1)) q(i)"},
		{"static_into_utf8_trivia", "SELECT /* é */\n  dg_static_leaf() INTO s\n FROM (VALUES(1)) q(i)", "SELECT /* é */\n  dg_static_leaf()"+strings.Repeat(" ",9)+"FROM (VALUES(1)) q(i)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t,p,b,c,
					"CREATE FUNCTION dg_static_leaf() RETURNS text LANGUAGE plpgsql AS $$DECLARE s text; BEGIN GET DIAGNOSTICS s=PG_CONTEXT; RETURN s; END$$",
					"CREATE FUNCTION dg_static_parent() RETURNS text LANGUAGE plpgsql AS $$\nDECLARE s text;\nBEGIN\n "+tc.statement+";\n RETURN s;\nEND$$")
				want := diagnosticsStaticLeaf+"\nSQL statement \""+tc.query+"\"\nPL/pgSQL function dg_static_parent() line 4 at SQL statement"
				sqlExecutionValue(t,p,b,c,"SELECT dg_static_parent() AS v","TEXT",want)
				var retained string
				if err:=c.QueryRowContext(p.Ctx,"SELECT dg_static_parent()").Scan(&retained);err!=nil { t.Fatal(err) }
				sqlExecutionValue(t,p,b,c,"SELECT dg_static_leaf() AS v","TEXT",diagnosticsStaticLeaf)
				if retained!=want { t.Fatalf("retained context=%q want=%q",retained,want) }
				if tc.name=="static_into_suffix" {
					userOperatorExec(t,p,b,c,"CREATE FUNCTION dg_static_sql() RETURNS text LANGUAGE SQL AS $$SELECT 'first'; SELECT dg_static_parent()$$","CREATE FUNCTION dg_static_outer() RETURNS text LANGUAGE plpgsql AS $$BEGIN RETURN dg_static_sql(); END$$")
					sqlExecutionValue(t,p,b,c,"SELECT dg_static_outer() AS v","TEXT",want+"\nSQL function \"dg_static_sql\" statement 2\nPL/pgSQL function dg_static_outer() line 1 at RETURN")
				}
			})
		})
	}
	for _, tc:=range []struct{ name, statement, query string }{
		{"static_perform","PERFORM dg_static_record_leaf()","SELECT dg_static_record_leaf()"},
		{"static_perform_spelling","pErFoRm /* é tail */ dg_static_record_leaf()","SELECT /* é tail */ dg_static_record_leaf()"},
	} {
		t.Run(tc.name,func(t *testing.T){ diagnosticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
			userOperatorExec(t,p,b,c,"CREATE TABLE dg_static_contexts(s text)",
				"CREATE FUNCTION dg_static_record_leaf() RETURNS text LANGUAGE plpgsql AS $$DECLARE s text; BEGIN GET DIAGNOSTICS s=PG_CONTEXT; INSERT INTO dg_static_contexts VALUES(s); RETURN s; END$$",
				"CREATE FUNCTION dg_static_perform() RETURNS text LANGUAGE plpgsql AS $$\nDECLARE s text;\nBEGIN\n "+tc.statement+";\n GET DIAGNOSTICS s=PG_CONTEXT;\n RETURN s;\nEND$$")
			for _,prepared:=range []bool{false,true}{
				s:=sqlExecutionStep(b,"SELECT dg_static_perform() AS v",[]string{"v"},[]string{"TEXT"},[][]any{{"PL/pgSQL function dg_static_perform() line 5 at GET DIAGNOSTICS"}});s.Prepared=prepared
				fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
				row:=sqlExecutionStep(b,"SELECT s AS v FROM dg_static_contexts",[]string{"v"},[]string{"TEXT"},[][]any{{"PL/pgSQL function dg_static_record_leaf() line 1 at GET DIAGNOSTICS\nSQL statement \""+tc.query+"\"\nPL/pgSQL function dg_static_perform() line 4 at PERFORM"}})
				row.Prepared=prepared;row.CheckMetadata=sqlExecutionMetadata(b,[]string{"v"},[]string{"TEXT"},true)
				fixture.RunStep(t,p.Ctx,c,b.Recorder,row)
				fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("DELETE FROM dg_static_contexts",1))
			}
		})})
	}
	t.Run("static_after_completion",func(t *testing.T){diagnosticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		userOperatorExec(t,p,b,c,"CREATE FUNCTION dg_static_leaf() RETURNS text LANGUAGE plpgsql AS $$DECLARE s text; BEGIN GET DIAGNOSTICS s=PG_CONTEXT; RETURN s; END$$",
			"CREATE FUNCTION dg_static_completed() RETURNS text LANGUAGE plpgsql AS $$\nDECLARE s text; a text;\nBEGIN\n SELECT dg_static_leaf() INTO s;\n GET DIAGNOSTICS a=PG_CONTEXT;\n s:=dg_static_leaf();\n RETURN a || '|' || s;\nEND$$")
		sqlExecutionValue(t,p,b,c,"SELECT dg_static_completed() AS v","TEXT","PL/pgSQL function dg_static_completed() line 5 at GET DIAGNOSTICS|"+diagnosticsStaticLeaf+"\nPL/pgSQL function dg_static_completed() line 6 at assignment")
	})})
	for _,tc:=range []struct{ name, statement, query, action string }{
		{"static_capture_failure_restoration","SELECT dg_static_error(0) INTO x","SELECT dg_static_error(0)","SQL statement"},
		{"static_perform_failure_restoration","PERFORM dg_static_error(0)","SELECT dg_static_error(0)","PERFORM"},
	}{ t.Run(tc.name,func(t *testing.T){diagnosticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		userOperatorExec(t,p,b,c,
			"CREATE FUNCTION dg_static_leaf() RETURNS text LANGUAGE plpgsql AS $$DECLARE s text; BEGIN GET DIAGNOSTICS s=PG_CONTEXT; RETURN s; END$$",
			"CREATE FUNCTION dg_static_error(z integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 1/z; END$$",
			"CREATE FUNCTION dg_static_caught() RETURNS text LANGUAGE plpgsql AS $$\nDECLARE x integer; a text; b text; c text;\nBEGIN\n BEGIN\n  "+tc.statement+";\n EXCEPTION WHEN division_by_zero THEN\n  GET STACKED DIAGNOSTICS a=PG_EXCEPTION_CONTEXT,b=PG_CONTEXT;\n END;\n c:=dg_static_leaf();\n RETURN a || '|' || b || '|' || c;\nEND$$",
			"CREATE FUNCTION dg_static_failed() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN "+tc.statement+"; RETURN x; END$$")
		prefix:="PL/pgSQL function dg_static_error(integer) line 1 at RETURN\nSQL statement \""+tc.query+"\"\n"
		sqlExecutionValue(t,p,b,c,"SELECT dg_static_caught() AS v","TEXT",prefix+"PL/pgSQL function dg_static_caught() line 5 at "+tc.action+"|PL/pgSQL function dg_static_caught() line 7 at GET STACKED DIAGNOSTICS|"+diagnosticsStaticLeaf+"\nPL/pgSQL function dg_static_caught() line 9 at assignment")
		d:=plpgsqlRuntimeDiagnostic("22012","division by zero","dg_static_failed()",tc.action);d.Context=prefix+d.Context
		for _,prepared:=range []bool{false,true}{s:=fixture.ErrorDiagnostic("SELECT dg_static_failed() AS v",d);s.SelectRows,s.Prepared,s.ErrorStage=true,prepared,"execute";fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
	})}) }
}

func diagnosticsStaticCaptureBoundary(t *testing.T) {
	for _,tc:=range []struct{name,statement,condition,want,code,message,hint string}{
		{"static_late_conversion","SELECT 'bad'::text INTO x;","invalid_text_representation","1:true;99","22P02","invalid input syntax for type integer: \"bad\"",""},
		{"static_late_cardinality","SELECT i INTO STRICT x FROM (VALUES(1),(2),(3)) q(i);","too_many_rows","2:true;99","P0003","query returned more than one row","Make sure the query returns a single row, or use LIMIT 1."},
	}{t.Run(tc.name,func(t *testing.T){diagnosticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		userOperatorExec(t,p,b,c,
			"CREATE FUNCTION dg_static_boundary() RETURNS text LANGUAGE plpgsql AS $$\nDECLARE n bigint; x integer:=99; s text:=''; a text; b text;\nBEGIN\n BEGIN\n  "+tc.statement+"\n EXCEPTION WHEN "+tc.condition+" THEN\n  GET DIAGNOSTICS n=ROW_COUNT; s:=n::text || ':' || found::text || ';' || x::text;\n  GET STACKED DIAGNOSTICS a=PG_EXCEPTION_CONTEXT,b=PG_CONTEXT;\n END;\n RETURN s || '|' || a || '|' || b;\nEND$$",
			"CREATE FUNCTION dg_static_boundary_failed() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer:=99; BEGIN "+tc.statement+" RETURN x; END$$")
		sqlExecutionValue(t,p,b,c,"SELECT dg_static_boundary() AS v","TEXT",tc.want+"|PL/pgSQL function dg_static_boundary() line 5 at SQL statement|PL/pgSQL function dg_static_boundary() line 8 at GET STACKED DIAGNOSTICS")
		d:=plpgsqlRuntimeDiagnostic(tc.code,tc.message,"dg_static_boundary_failed()","SQL statement");d.Hint=tc.hint
		for _,prepared:=range []bool{false,true}{s:=fixture.ErrorDiagnostic("SELECT dg_static_boundary_failed() AS v",d);s.SelectRows,s.Prepared,s.ErrorStage=true,prepared,"execute";fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
	})})}
}
