package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAutoincLateBinding(t *testing.T) {
	t.Run("temporary-sequence",autoincTemporaryBinding)
	for _,tc:=range []struct{ name,arg,code,message string }{
		{"missing","missing","42P01",`relation "missing" does not exist`},
		{"malformed","a..s","42602","invalid name syntax"},
		{"numeric","123","42P01",`relation "123" does not exist`},
		{"dash","-","42P01",`relation "-" does not exist`},
	} {
		for _,route:=range []string{"direct","prepared","held"} {
		t.Run(tc.name+"/"+route,func(t *testing.T) {
			triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				userOperatorExec(t,p,b,c,"CREATE EXTENSION autoinc","CREATE TABLE t(id integer,label text)","CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc('id','"+tc.arg+"')")
				q:="INSERT INTO t VALUES($1,$2)"
				run:=autoincRoute(t,p,b,c,route,q)
				run(fixture.Exec(q,1,int64(7),"retained"))
				s:=autoincError(q,tc.code,tc.message);s.Args=[]any{int64(0),"failed"};run(s)
				run(autoincRows("SELECT id,label FROM t ORDER BY label",[]any{int64(7),"retained"}))
				if tc.name=="numeric" || tc.name=="dash" {
					userOperatorExec(t,p,b,c,`CREATE SEQUENCE "`+tc.arg+`" START 70`)
					run(fixture.Exec(q,1,nil,"named"))
					run(autoincRows("SELECT id,label FROM t ORDER BY label",[]any{int64(70),"named"},[]any{int64(7),"retained"}))
				}
			})
		})
		}
	}
	t.Run("numeric-existing-oid",func(t *testing.T) {
		triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			userOperatorExec(t,p,b,c,"CREATE EXTENSION autoinc","CREATE SEQUENCE real_sequence","CREATE TABLE t(id integer)")
			var oid string
			if err:=c.QueryRowContext(p.Ctx,"SELECT 'real_sequence'::regclass::oid::text").Scan(&oid);err!=nil { t.Fatal(err) }
			userOperatorExec(t,p,b,c,"CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc('id','"+oid+"')")
			fixture.RunStep(t,p.Ctx,c,b.Recorder,autoincError("INSERT INTO t VALUES(0)","42P01",`relation "`+oid+`" does not exist`))
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT is_called FROM real_sequence",[]string{"is_called"},[]string{"BOOL"},[][]any{{false}}))
		})
	})
	t.Run("path-function-identity",func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t,"ai_a");fixture.SchemaReferenceCleanup(t,"ai_b")
		triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			userOperatorExec(t,p,b,c,"CREATE SCHEMA ai_a","CREATE SCHEMA ai_b","CREATE EXTENSION autoinc","CREATE SEQUENCE ai_a.s START 10","CREATE SEQUENCE ai_b.s START 100","CREATE TABLE public.t(id integer,label text)","CREATE TRIGGER a BEFORE INSERT ON public.t FOR EACH ROW EXECUTE FUNCTION public.autoinc('id','s')")
			q:="INSERT INTO public.t VALUES($1,$2) RETURNING id,label"
			run:=autoincRoute(t,p,b,c,"held",q)
			insert:=func(label string,n int64) { s:=autoincRows(q,[]any{n,label});s.Args=[]any{nil,label};run(s) }
			run(fixture.Exec("SET search_path=ai_a,public",0));insert("a",10)
			run(fixture.Exec("SET search_path=ai_b,public",0));insert("b",100)
			run(fixture.Exec("ALTER SEQUENCE ai_b.s RENAME TO old_s",0))
			s:=autoincError(q,"42P01",`relation "s" does not exist`);s.SelectRows=true;s.Args=[]any{nil,"failed"};run(s)
			run(fixture.Exec("CREATE SEQUENCE ai_b.s START 70",0));insert("c",70)
			userOperatorExec(t,p,b,c,"ALTER FUNCTION public.autoinc() RENAME TO real_autoinc","ALTER FUNCTION public.real_autoinc() SET SCHEMA ai_a","CREATE FUNCTION public.autoinc() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.id:=999; RETURN NEW; END$$")
			insert("d",71)
			run(fixture.Exec("ALTER EXTENSION autoinc DROP FUNCTION ai_a.real_autoinc()",0));insert("e",72)
			run(fixture.Exec("ALTER EXTENSION autoinc ADD FUNCTION ai_a.real_autoinc()",0));insert("f",73)
			run(fixture.Select("SELECT t.tgfoid=p.oid AS same FROM pg_trigger t JOIN pg_proc p ON p.oid='ai_a.real_autoinc()'::regprocedure WHERE t.tgrelid='public.t'::regclass",[]string{"same"},[]string{"BOOL"},[][]any{{true}}))
		})
	})
	t.Run("quoted-qualified",func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t,"Ai Quotes")
		triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			userOperatorExec(t,p,b,c,"CREATE EXTENSION autoinc",`CREATE SCHEMA "Ai Quotes"`,`CREATE SEQUENCE "Ai Quotes"."S ' Q" START 80`,"CREATE TABLE t(id integer,label text)",`CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc('id','"Ai Quotes"."S '' Q"')`)
			fixture.RunStep(t,p.Ctx,c,b.Recorder,autoincRows("INSERT INTO t VALUES(0,'q') RETURNING id,label",[]any{int64(80),"q"}))
		})
	})
}

func TestReferenceAutoincPreparedMetadata(t *testing.T) {
	t.Run("retained",func(t *testing.T) {
		triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			autoincSeed(t,p,b,c)
			q:="INSERT INTO t VALUES($1,$2) RETURNING id,label"
			run:=autoincRoute(t,p,b,c,"held",q)
			run(fixture.Select("SELECT is_called FROM s",[]string{"is_called"},[]string{"BOOL"},[][]any{{false}}))
			selectSQL:="SELECT id,label FROM t ORDER BY label"
			read:=autoincRoute(t,p,b,c,"held",selectSQL)
			metadata:=func(t *testing.T,rows *sql.Rows) {
				cols,err:=rows.ColumnTypes();if err!=nil { t.Fatal(err) }
				if len(cols)!=2 { t.Fatal(cols) }
				for _,col:=range cols { nullable,known:=col.Nullable();if known==b.PG || (!b.PG && !nullable) { t.Fatalf("nullability %s=%v,%v",col.Name(),nullable,known) } }
				want:=reflect.TypeOf(int64(0));if b.PG { want=reflect.TypeOf(int32(0)) };if cols[0].ScanType()!=want { t.Fatal(cols[0].ScanType()) }
			}
			s:=autoincRows(selectSQL);s.CheckMetadata=metadata;read(s)
			s=autoincRows(q,[]any{int64(10),"a"});s.Args=[]any{nil,"a"};s.CheckMetadata=metadata;run(s)
			userOperatorExec(t,p,b,c,"ALTER FUNCTION autoinc() RENAME TO renamed_autoinc","CREATE FUNCTION autoinc() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.id:=999; RETURN NEW; END$$")
			s=autoincRows(q,[]any{int64(20),"b"});s.Args=[]any{int64(0),"b"};run(s)
			s=autoincRows(selectSQL,[]any{int64(10),"a"},[]any{int64(20),"b"});s.CheckMetadata=metadata;read(s)
			run(fixture.Exec("BEGIN",0));s=autoincRows(q,[]any{int64(30),"rolled"});s.Args=[]any{nil,"rolled"};run(s);run(fixture.Exec("ROLLBACK",0))
			s=autoincRows(q,[]any{int64(40),"c"});s.Args=[]any{nil,"c"};run(s)
			ctx,cancel:=context.WithCancel(p.Ctx);cancel()
			// Cancel before database/sql acquires a physical session. A borrowed
			// pq Conn can complete a fast command despite an already-done context.
			_,err:=b.DB.ExecContext(ctx,"INSERT INTO t VALUES(NULL,'canceled')")
			if !errors.Is(err,context.Canceled) { t.Fatal(err) }
			run(autoincScalar("SELECT currval('s')","currval",40))
		})
	})
	t.Run("current-authority",func(t *testing.T) {
		fixture.RoleReferenceCleanup(t,[]string{"ai_writer"});fixture.SchemaReferenceCleanup(t,"ai_private")
		triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			userOperatorExec(t,p,b,c,"CREATE EXTENSION autoinc","CREATE ROLE ai_writer","CREATE SCHEMA ai_private","CREATE SEQUENCE ai_private.s START 10","CREATE TABLE t(id integer,label text)","CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc('id','ai_private.s')","GRANT USAGE ON SCHEMA public,ai_private TO ai_writer","GRANT INSERT,SELECT ON t TO ai_writer","GRANT USAGE ON SEQUENCE ai_private.s TO ai_writer")
			peer,err:=b.DB.Conn(p.Ctx);if err!=nil { t.Fatal(err) };defer peer.Close()
			userOperatorExec(t,p,b,c,"SET ROLE ai_writer");defer c.ExecContext(p.Ctx,"RESET ROLE")
			q:="INSERT INTO t VALUES($1,$2)"
			run:=autoincRoute(t,p,b,c,"held",q)
			run(fixture.Exec(q,1,nil,"a"))
			userOperatorExec(t,p,b,peer,"REVOKE USAGE ON SEQUENCE ai_private.s FROM ai_writer")
			s:=autoincError(q,"42501","permission denied for sequence s");s.Args=[]any{nil,"failed"};run(s)
			run(fixture.Exec(q,1,int64(7),"nonzero"))
			userOperatorExec(t,p,b,peer,"GRANT UPDATE ON SEQUENCE ai_private.s TO ai_writer")
			run(fixture.Exec(q,1,int64(0),"b"))
			// Existing triggers retain their identity despite EXECUTE revocation.
			userOperatorExec(t,p,b,peer,"REVOKE EXECUTE ON FUNCTION autoinc() FROM PUBLIC","GRANT TRIGGER ON t TO ai_writer")
			run(fixture.Exec(q,1,int64(7),"revoked-helper-existing"))
			run(autoincError("CREATE TRIGGER denied BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc('id','ai_private.s')","42501","permission denied for function autoinc"))
			userOperatorExec(t,p,b,peer,"REVOKE USAGE ON SCHEMA ai_private FROM ai_writer")
			s=autoincError(q,"42501","permission denied for schema ai_private");s.Args=[]any{nil,"schema-failed"};run(s)
			run(fixture.Exec(q,1,int64(8),"schema-bypass"))
			userOperatorExec(t,p,b,peer,"REVOKE INSERT ON t FROM ai_writer")
			s=autoincError(q,"42501","permission denied for table t");s.Args=[]any{int64(9),"table-failed"};run(s)
			fixture.RunStep(t,p.Ctx,peer,b.Recorder,autoincScalar("SELECT last_value FROM ai_private.s","last_value",11))
		})
	})
}

func autoincTemporaryBinding(t *testing.T) {
	triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		userOperatorExec(t,p,b,c,"CREATE EXTENSION autoinc","CREATE SEQUENCE public.s START 10","CREATE TEMP SEQUENCE s START 100","CREATE TABLE t(id integer,label text)","CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc('id','s')")
		q:="INSERT INTO t VALUES($1,$2) RETURNING id,label"
		run:=autoincRoute(t,p,b,c,"held",q)
		s:=autoincRows(q,[]any{int64(100),"temporary"});s.Args=[]any{nil,"temporary"};run(s)
		run(fixture.Exec("DROP SEQUENCE pg_temp.s",0))
		s=autoincRows(q,[]any{int64(10),"permanent"});s.Args=[]any{nil,"permanent"};run(s)
	})
}
