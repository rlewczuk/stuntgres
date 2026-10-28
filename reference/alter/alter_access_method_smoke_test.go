package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAlterAccessMethodSmoke(t *testing.T) {
	accessMethodScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		persistenceExec(t,p,b,c,"CREATE TABLE t(id int NOT NULL,note text)")
		persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO t VALUES(7,'keep')",1))
		before:=accessMethodIdentity(t,p,c,"t")
		selectSQL,insertSQL:="SELECT note FROM t WHERE id=$1","INSERT INTO t VALUES($1,$2)"
		selected,err:=c.PrepareContext(p.Ctx,selectSQL);if err!=nil {t.Fatal(err)};defer selected.Close()
		inserted,err:=c.PrepareContext(p.Ctx,insertSQL);if err!=nil {t.Fatal(err)};defer inserted.Close()
		persistenceRun(t,p,b,c,fixture.Prepared(fixture.Exec("ALTER TABLE t SET ACCESS METHOD heap2",0)))
		accessMethodProperty(t,p,b,c,"t","heap2")
		after:=accessMethodIdentity(t,p,c,"t")
		if before[0]!=after[0] || before[1]!=after[1] {t.Fatal("table or rowtype identity changed",before,after)}
		persistenceRun(t,p,b,fixture.HeldStatement{Executor:c,Statement:inserted,SQL:insertSQL},fixture.Exec(insertSQL,1,8,"new"))
		persistenceRun(t,p,b,fixture.HeldStatement{Executor:c,Statement:selected,SQL:selectSQL},fixture.Select(selectSQL,[]string{"note"},[]string{"TEXT"},[][]any{{"keep"}},7))
		persistenceExec(t,p,b,c,"SET default_table_access_method=heap3")
		persistenceExec(t,p,b,c,"ALTER TABLE t SET ACCESS METHOD DEFAULT")
		accessMethodProperty(t,p,b,c,"t","heap3")
		want:=persistenceDiagnostic("23514",`check constraint "bad" of relation "t" is violated by some row`);want.Schema,want.Table,want.Constraint="public","t","bad"
		persistenceFailure(t,p,b,c,"ALTER TABLE t SET ACCESS METHOD heap2, ADD COLUMN added int DEFAULT 9, ADD CONSTRAINT bad CHECK(id<0)",true,want)
		accessMethodProperty(t,p,b,c,"t","heap3")
		persistenceRun(t,p,b,c,fixture.Select("SELECT * FROM t ORDER BY id",[]string{"id","note"},[]string{"INT4","TEXT"},[][]any{{int64(7),"keep"},{int64(8),"new"}}))
		for _,q:=range []string{"CREATE TABLE p(id int) PARTITION BY RANGE(id)","ALTER TABLE p SET ACCESS METHOD heap2","CREATE TABLE p0 PARTITION OF p FOR VALUES FROM(0) TO(10)","ALTER TABLE p SET ACCESS METHOD DEFAULT","CREATE TABLE p1 PARTITION OF p FOR VALUES FROM(10) TO(20)"} {persistenceExec(t,p,b,c,q)}
		check:=func(run func(fixture.Step)) {
			run(accessMethodStep("t","heap3"));run(accessMethodStep("p",""));run(accessMethodStep("p0","heap2"));run(accessMethodStep("p1","heap3"))
			run(accessMethodRows([][]any{{int64(7),"keep"},{int64(8),"new"}}))
		}
		check(func(step fixture.Step){persistenceRun(t,p,b,c,step)})
		if !b.PG {
			accessMethodControlRoundtrip(t,p,check)
			if p.Postgres!=nil {
				// Finish replay cleanup before the next backend even if replay
				// or an assertion fails, independently of the scenario context.
				defer func() {
					ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second);defer cancel()
					for _,q:=range []string{"DROP TABLE IF EXISTS t,p CASCADE","DROP ACCESS METHOD IF EXISTS heap2","DROP ACCESS METHOD IF EXISTS heap3"} {
						if _,err:=p.Postgres.ExecContext(ctx,q);err!=nil {t.Errorf("PostgreSQL smoke replay cleanup: %v",err)}
					}
				}()
				var dump bytes.Buffer
				if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
				if _,err:=p.Postgres.ExecContext(p.Ctx,dump.String());err!=nil {t.Fatal("PostgreSQL smoke replay",err)}
				for _,oracle:=range p.Backends() {if oracle.PG {check(func(step fixture.Step){persistenceRun(t,p,oracle,p.Postgres,step)})}}
			}
		}
	})
}
