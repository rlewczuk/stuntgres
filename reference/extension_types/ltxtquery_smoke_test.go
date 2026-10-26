package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceLtxtqueryFeatureSmoke(t *testing.T) {
	ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		ltreeInstall(t,p,b,c)
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
		run(fixture.Exec("CREATE TABLE lx_smoke(id integer NOT NULL,v ltxtquery)",0))
		run(fixture.Exec("CREATE TABLE lx_old(t ltree,q lquery)",0))
		run(fixture.Exec("INSERT INTO lx_old VALUES ('a.b','a*@%')",1))
		run(fixture.Exec("INSERT INTO lx_smoke VALUES (1,' Europe&Russia*@&!Transportation '),(3,NULL)",2))
		q:="INSERT INTO lx_smoke VALUES ($1,$2)"
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Exec(q,1,2,"foo*@%@@**"))
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Exec(q,1,4,nil))
		check:=ltxtquerySelect(b,"SELECT id,v FROM lx_smoke ORDER BY id",[]string{"id","v"},[][]any{{int64(1),"Europe & Russia@* & !Transportation"},{int64(2),"foo%@*"},{int64(3),nil},{int64(4),nil}})
		check.CheckMetadata=func(t *testing.T,rows *sql.Rows){ltxtqueryMetadata(t,b.PG,rows,check.Columns,[]bool{false,true})}
		run(check)
		bad:=queryIntParameterDiagnostic("42601","modifiers syntax error","foo@bar",2)
		ltreeError(t,p,b,c,"INSERT INTO lx_smoke VALUES (5,$1),(6,$2)",true,false,false,bad,"a","foo@bar")
		run(check)
		empty:=queryIntParameterDiagnostic("42601","operand syntax error","",1)
		ltreeError(t,p,b,c,"SELECT $1::ltxtquery AS v",true,true,false,empty,"")
		if !b.PG {
			old,err:=b.DB.QueryContext(p.Ctx,check.Query);if err!=nil {t.Fatal(err)};defer old.Close()
			metadata:=ltxtqueryMetadata(t,false,old,check.Columns,[]bool{false,true})
			var dump bytes.Buffer
			if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
			if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
			if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)}
			run(check);ltxtqueryConsumeRetained(t,old,check.Rows)
			ltxtqueryColumnMetadata(t,false,metadata,check.Columns,[]bool{false,true})
			if p.Postgres!=nil {
				if _,err:=p.Postgres.ExecContext(p.Ctx,"DROP SCHEMA public CASCADE; CREATE SCHEMA public");err!=nil {t.Fatal(err)}
				if _,err:=p.Postgres.ExecContext(p.Ctx,dump.String());err!=nil {t.Fatal("actual dump replay",err)}
				pg:=fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord}
				fixture.RunStep(t,p.Ctx,pg.DB,pg.Recorder,ltxtquerySelect(pg,check.Query,check.Columns,check.Rows))
				ltxtqueryIdentityChecks(t,p,pg,pg.DB)
				if _,err:=p.Postgres.ExecContext(p.Ctx,"DROP SCHEMA public CASCADE; CREATE SCHEMA public");err!=nil {t.Fatal(err)}
			}
		}
		literalSQL:="INSERT INTO lx_smoke VALUES (5,'a|b&c')"
		boundSQL:="INSERT INTO lx_smoke VALUES (6,$1::text::ltxtquery)"
		literal,err:=c.PrepareContext(p.Ctx,literalSQL);if err!=nil {t.Fatal(err)};defer literal.Close()
		bound,err:=c.PrepareContext(p.Ctx,boundSQL);if err!=nil {t.Fatal(err)};defer bound.Close()
		var before,after int64
		if err:=c.QueryRowContext(p.Ctx,"SELECT 'ltxtquery'::regtype::oid").Scan(&before);err!=nil {t.Fatal(err)}
		var retained *sql.Rows
		var detached []*sql.ColumnType
		if !b.PG {
			retained,err=b.DB.QueryContext(p.Ctx,check.Query);if err!=nil {t.Fatal(err)};defer retained.Close()
			detached=ltxtqueryMetadata(t,false,retained,check.Columns,[]bool{false,true})
		}
		for _,q:=range []string{"DROP TABLE lx_smoke","DROP TABLE lx_old","DROP EXTENSION ltree","CREATE EXTENSION ltree","CREATE TABLE lx_smoke(id integer NOT NULL,v ltxtquery)","CREATE TABLE lx_old(t ltree,q lquery)"} {run(fixture.Exec(q,0))}
		if err:=c.QueryRowContext(p.Ctx,"SELECT 'ltxtquery'::regtype::oid").Scan(&after);err!=nil {t.Fatal(err)}
		if before<=0 || after<=0 || before==after {t.Fatal("recreation identity",before,after)}
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:literal,SQL:literalSQL},b.Recorder,fixture.Exec(literalSQL,1))
		for _,arg:=range []any{"foo*@%",nil} {fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:bound,SQL:boundSQL},b.Recorder,fixture.Exec(boundSQL,1,arg))}
		run(ltxtquerySelect(b,"SELECT v FROM lx_smoke WHERE id=5",[]string{"v"},[][]any{{"a | b & c"}}))
		s:=ltxtquerySelect(b,"SELECT v FROM lx_smoke WHERE id=6",[]string{"v"},[][]any{{"foo%@*"},{nil}});s.Bag=true;run(s)
		run(fixture.Exec("INSERT INTO lx_old VALUES ('a.b','a*@%')",1))
		run(fixture.Select("SELECT t::text AS t,q::text AS q FROM lx_old",[]string{"t","q"},[]string{"TEXT","TEXT"},[][]any{{"a.b","a%@*"}}))
		ltxtqueryIdentityChecks(t,p,b,c)
		if retained!=nil {ltxtqueryConsumeRetained(t,retained,check.Rows);ltxtqueryColumnMetadata(t,false,detached,check.Columns,[]bool{false,true})}
	})
}
