package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Unset portable fields deliberately assert absence. Root's pinned oracle must
// resolve these source-grounded records before implementation authorization.
func autoincError(q, code, message string) fixture.Step {
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(code,message))
	s.ErrorStage = "execute"
	return s
}

func TestReferenceAutoincArguments(t *testing.T) {
	cases := []struct{ name, typ, args, code, message string }{
		{"zero","integer","","XX000","autoinc (t): even number gt 0 of arguments was expected"},
		{"odd","integer","'id'","XX000","autoinc (t): even number gt 0 of arguments was expected"},
		{"missing","integer","'missing','s'","09000",`"t" has no attribute "missing"`},
		{"system","integer","'ctid','s'","09000",`"t" has no attribute "ctid"`},
		{"int2","smallint","'id','s'","09000",`attribute "id" of "t" must be type INT4`},
		{"int8","bigint","'id','s'","09000",`attribute "id" of "t" must be type INT4`},
		{"domain","key_domain","'id','s'","09000",`attribute "id" of "t" must be type INT4`},
	}
	for _, tc := range cases {
		for _, route := range []string{"direct","prepared","held"} {
			t.Run(tc.name+"/"+route, func(t *testing.T) {
				triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
					userOperatorExec(t,p,b,c,"CREATE EXTENSION autoinc","CREATE SEQUENCE s","CREATE DOMAIN key_domain AS integer","CREATE TABLE t(id "+tc.typ+")","CREATE TRIGGER a BEFORE INSERT OR UPDATE ON t FOR EACH ROW EXECUTE FUNCTION autoinc("+tc.args+")")
					q := "INSERT INTO t VALUES($1)"
					run := autoincRoute(t,p,b,c,route,q)
					run(fixture.Exec("UPDATE t SET id=0 WHERE false",0))
					for _, value := range []any{nil,int64(0),int64(7)} {
						s := autoincError(q,tc.code,tc.message); s.Args=[]any{value}; run(s)
					}
					run(autoincScalar("SELECT count(*) FROM t","count",0))
					run(fixture.Select("SELECT is_called FROM s",[]string{"is_called"},[]string{"BOOL"},[][]any{{false}}))
				})
			})
		}
	}
	for _, arg := range []string{"MiXeD","mixed",`"MiXeD"`} {
		t.Run("literal/"+arg,func(t *testing.T) {
			triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				userOperatorExec(t,p,b,c,"CREATE EXTENSION autoinc","CREATE SEQUENCE s START 10",`CREATE TABLE t("MiXeD" integer)`,"CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc('"+arg+"','s')")
				run := autoincRoute(t,p,b,c,"direct","")
				if arg=="MiXeD" {
					run(fixture.Select(`INSERT INTO t VALUES(NULL) RETURNING "MiXeD"`,[]string{"MiXeD"},[]string{"INT4"},[][]any{{int64(10)}}))
					run(fixture.Prepared(fixture.Select(`INSERT INTO t VALUES(0) RETURNING "MiXeD"`,[]string{"MiXeD"},[]string{"INT4"},[][]any{{int64(11)}})))
				} else { run(autoincError("INSERT INTO t VALUES(7)","09000",`"t" has no attribute "`+arg+`"`)) }
			})
		})
	}
}

func TestReferenceAutoincDiagnostics(t *testing.T) {
	t.Run("relation-kind",autoincRelationKind)
	t.Run("read-only-demand",autoincReadOnlyDemand)
	for _, tc := range []struct{ name, declaration, q, message string }{
		{"statement","BEFORE INSERT ON t FOR EACH STATEMENT","INSERT INTO t VALUES(0,'x')","must be fired for row"},
		{"after-statement","AFTER INSERT ON t FOR EACH STATEMENT","INSERT INTO t VALUES(0,'x')","must be fired for row"},
		{"after-row","AFTER INSERT ON t FOR EACH ROW","INSERT INTO t VALUES(0,'x')","must be fired before event"},
		{"delete","BEFORE DELETE ON t FOR EACH ROW","DELETE FROM t","cannot process DELETE events"},
	} {
		for _,route := range []string{"direct","prepared","held"} {
			t.Run(tc.name+"/"+route,func(t *testing.T) {
				triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
					userOperatorExec(t,p,b,c,"CREATE EXTENSION autoinc","CREATE SEQUENCE s START 10","CREATE TABLE t(id integer,label text)")
					fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO t VALUES(7,'old')",1))
					userOperatorExec(t,p,b,c,"CREATE TRIGGER a "+tc.declaration+" EXECUTE FUNCTION autoinc()")
					run:=autoincRoute(t,p,b,c,route,tc.q)
					run(autoincError(tc.q,"XX000",tc.message))
					run(autoincRows("SELECT id,label FROM t ORDER BY label",[]any{int64(7),"old"}))
					run(fixture.Select("SELECT pg_trigger_depth() AS depth",[]string{"depth"},[]string{"INT4"},[][]any{{int64(0)}}))
					userOperatorExec(t,p,b,c,"DROP TRIGGER a ON t","CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc('id','s')")
					run(autoincRows("INSERT INTO t VALUES(NULL,'healthy') RETURNING id,label",[]any{int64(10),"healthy"}))
				})
			})
		}
	}
	t.Run("scalar",func(t *testing.T) {
		triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			userOperatorExec(t,p,b,c,"CREATE EXTENSION autoinc")
			for _,route:=range []string{"direct","prepared","held"} {
				q:="SELECT autoinc()"
				run:=autoincRoute(t,p,b,c,route,q)
				s:=autoincError(q,"XX000","not fired by trigger manager");s.SelectRows=true;run(s)
			}
			// A scalar call inside an active PL trigger must not borrow its frame.
			userOperatorExec(t,p,b,c,"CREATE TABLE t(id integer)","CREATE FUNCTION nested_call() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM autoinc(); RETURN NEW; END$$","CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION nested_call()")
			s:=autoincError("INSERT INTO t VALUES(0)","XX000","not fired by trigger manager")
			s.Diagnostic.Context="SQL statement \"SELECT autoinc()\"\nPL/pgSQL function nested_call() line 1 at PERFORM"
			fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
		})
	})
	for _,route:=range []string{"direct","prepared","held"} {
	t.Run("sequence-errors/"+route,func(t *testing.T) {
		triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			userOperatorExec(t,p,b,c,"CREATE EXTENSION autoinc","CREATE SEQUENCE s MAXVALUE 10 START 10","CREATE TABLE t(id integer,label text)","CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc('id','s')")
			run:=autoincRoute(t,p,b,c,route,"INSERT INTO t VALUES(0,'b')")
			run(fixture.Exec("INSERT INTO t VALUES(NULL,'a')",1))
			run(autoincError("INSERT INTO t VALUES(0,'b')","2200H",`nextval: reached maximum value of sequence "s" (10)`))
			run(fixture.Exec("BEGIN READ ONLY",0))
			run(autoincError("INSERT INTO t VALUES(0,'b')","25006","cannot execute INSERT in a read-only transaction"))
			run(fixture.Exec("ROLLBACK",0))
			run(autoincRows("SELECT id,label FROM t ORDER BY label",[]any{int64(10),"a"}))
		})
	})
	}
}

func autoincRelationKind(t *testing.T) {
	for _,route:=range []string{"direct","prepared","held"} {
		t.Run(route,func(t *testing.T) {
			triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				userOperatorExec(t,p,b,c,"CREATE EXTENSION autoinc","CREATE TABLE s(n integer)","CREATE TABLE t(id integer,label text)","CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc('id','s')")
				q:="INSERT INTO t VALUES($1,$2)"
				run:=autoincRoute(t,p,b,c,route,q)
				run(fixture.Exec(q,1,int64(7),"nonzero"))
				s:=autoincError(q,"42809",`cannot open relation "s"`);s.Diagnostic.Detail="This operation is not supported for tables.";s.Args=[]any{nil,"bad"};run(s)
				run(autoincRows("SELECT id,label FROM t ORDER BY label",[]any{int64(7),"nonzero"}))
			})
		})
	}
}

func autoincReadOnlyDemand(t *testing.T) {
	for _,route:=range []string{"direct","prepared","held"} {
		t.Run(route,func(t *testing.T) {
			triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				userOperatorExec(t,p,b,c,"CREATE EXTENSION autoinc","CREATE SEQUENCE s START 10","CREATE TEMP TABLE t(id integer,label text)","CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc('id','public.s')")
				q:="INSERT INTO t VALUES($1,$2)"
				run:=autoincRoute(t,p,b,c,route,q)
				run(fixture.Exec("BEGIN READ ONLY",0))
				run(fixture.Exec(q,1,int64(7),"nonzero"))
				s:=autoincError(q,"25006","cannot execute nextval() in a read-only transaction");s.Args=[]any{nil,"failed"};run(s)
				run(fixture.Exec("ROLLBACK",0))
				run(autoincScalar("SELECT count(*) FROM t","count",0))
				run(fixture.Select("SELECT is_called FROM s",[]string{"is_called"},[]string{"BOOL"},[][]any{{false}}))
				run(fixture.Select("SELECT pg_trigger_depth() AS depth",[]string{"depth"},[]string{"INT4"},[][]any{{int64(0)}}))
				run(fixture.Exec(q,1,nil,"healthy"))
				run(autoincRows("SELECT id,label FROM t ORDER BY label",[]any{int64(10),"healthy"}))
			})
		})
	}
}
