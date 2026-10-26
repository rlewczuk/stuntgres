package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func ltxtqueryRenameMove(t *testing.T) {
	t.Helper()
	t.Run("rename_move",func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t,"lx_move")
		ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			ltreeInstall(t,p,b,c)
			run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
			run(fixture.Exec("CREATE TABLE lx_renamed(id integer NOT NULL,v ltxtquery)",0))
			run(fixture.Exec("INSERT INTO lx_renamed VALUES (1,'foo*@%'),(2,NULL)",2))
			var scalar,array,input,output int64
			if err:=c.QueryRowContext(p.Ctx,"SELECT oid,typarray,typinput::oid,typoutput::oid FROM pg_type WHERE oid='ltxtquery'::regtype::oid").Scan(&scalar,&array,&input,&output);err!=nil {t.Fatal(err)}
			if !b.PG {
				retained,err:=b.DB.QueryContext(p.Ctx,"SELECT id,v FROM lx_renamed ORDER BY id");if err!=nil {t.Fatal(err)}
				defer retained.Close()
				names,nullable:=[]string{"id","v"},[]bool{false,true}
				metadata:=ltxtqueryMetadata(t,false,retained,names,nullable)
				defer func(){
					ltxtqueryConsumeRetained(t,retained,[][]any{{int64(1),"foo%@*"},{int64(2),nil}})
					ltxtqueryColumnMetadata(t,false,metadata,names,nullable)
				}()
			}
			for _,q:=range []string{"CREATE SCHEMA lx_move","ALTER TYPE public.ltxtquery RENAME TO label_query","ALTER TYPE public.label_query SET SCHEMA lx_move"} {run(fixture.Exec(q,0))}
			check:=func(ex fixture.Executor,backend fixture.Backend) {
				fixture.RunStep(t,p.Ctx,ex,backend.Recorder,ltxtquerySelect(backend,"SELECT id,v FROM lx_renamed ORDER BY id",[]string{"id","v"},[][]any{{int64(1),"foo%@*"},{int64(2),nil}}))
				fixture.RunStep(t,p.Ctx,ex,backend.Recorder,fixture.Select("SELECT t.typname,a.typelem=t.oid AS reciprocal,a.typnamespace=t.typnamespace AS same_namespace,p.proname FROM pg_type t JOIN pg_type a ON a.oid=t.typarray JOIN pg_proc p ON p.oid=t.typoutput WHERE t.oid='lx_move.label_query'::regtype::oid",[]string{"typname","reciprocal","same_namespace","proname"},[]string{"NAME","BOOL","BOOL","NAME"},[][]any{{"label_query",true,true,"ltxtq_out"}}))
			}
			var nextScalar,nextArray,nextInput,nextOutput int64
			if err:=c.QueryRowContext(p.Ctx,"SELECT oid,typarray,typinput::oid,typoutput::oid FROM pg_type WHERE oid='lx_move.label_query'::regtype::oid").Scan(&nextScalar,&nextArray,&nextInput,&nextOutput);err!=nil {t.Fatal(err)}
			if scalar<=0 || scalar!=nextScalar || array!=nextArray || input!=nextInput || output!=nextOutput {t.Fatal("identity ALTER replaced logical identities")}
			run(fixture.Exec("INSERT INTO lx_renamed VALUES (3,$1::text::lx_move.label_query)",1,"a|b&c"))
			run(ltxtquerySelect(b,"SELECT v FROM lx_renamed WHERE id=3",[]string{"v"},[][]any{{"a | b & c"}}))
			run(fixture.Exec("DELETE FROM lx_renamed WHERE id=3",1));check(c,b)
			if !b.PG {
				var dump bytes.Buffer
				if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
				if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
				if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)}
				check(c,b)
				if p.Postgres!=nil {
					cleanup:="DROP SCHEMA IF EXISTS lx_move CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public"
					if _,err:=p.Postgres.ExecContext(p.Ctx,cleanup);err!=nil {t.Fatal(err)}
					if _,err:=p.Postgres.ExecContext(p.Ctx,dump.String());err!=nil {t.Fatal("renamed dump replay",err)}
					pg:=fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord};check(pg.DB,pg)
					if _,err:=p.Postgres.ExecContext(p.Ctx,cleanup);err!=nil {t.Fatal(err)}
				}
			}
		})
	})
}
