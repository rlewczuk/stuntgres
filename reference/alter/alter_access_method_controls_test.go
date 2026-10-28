package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func accessMethodControlSetup(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	for _,q:=range []string{
		"CREATE TYPE pair AS(id int,note text)","CREATE TABLE t OF pair",
		"ALTER TABLE t SET ACCESS METHOD heap2",
		"CREATE TABLE p(id int) PARTITION BY RANGE(id)","ALTER TABLE p SET ACCESS METHOD heap2",
		"CREATE TABLE p0 PARTITION OF p FOR VALUES FROM(0) TO(10)",
		"ALTER TABLE p0 SET ACCESS METHOD heap3",
		"CREATE TABLE attached(id int)","ALTER TABLE attached SET ACCESS METHOD heap",
		"ALTER TABLE p ATTACH PARTITION attached FOR VALUES FROM(10) TO(20)",
		"CREATE TABLE fallback(id int) PARTITION BY RANGE(id)",
		"CREATE TABLE fallback0 PARTITION OF fallback DEFAULT",
		"ALTER TABLE fallback0 SET ACCESS METHOD heap3",
	} {persistenceExec(t,p,b,c,q)}
	persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO t VALUES(7,'keep'),(8,NULL)",2))
	persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO p VALUES(1),(11)",2))
	persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO fallback VALUES(31)",1))
	persistenceExec(t,p,b,c,"CREATE TABLE nested_root(id int) PARTITION BY RANGE(id)")
	persistenceExec(t,p,b,c,"CREATE TABLE nested_child PARTITION OF nested_root DEFAULT PARTITION BY RANGE(id)")
	persistenceExec(t,p,b,c,"SET default_table_access_method=heap3")
	persistenceExec(t,p,b,c,"CREATE TABLE nested_leaf PARTITION OF nested_child DEFAULT")
	persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO nested_root VALUES(51)",1))
	persistenceRun(t,p,b,c,fixture.Exec("CREATE TABLE ctas AS SELECT id,note FROM t",2))
	persistenceExec(t,p,b,c,"SET default_table_access_method=heap")
	persistenceRun(t,p,b,c,fixture.Exec("CREATE MATERIALIZED VIEW mv AS SELECT id FROM t",2))
	for _,q:=range []string{
		"ALTER MATERIALIZED VIEW mv SET ACCESS METHOD heap3",
		"CREATE MATERIALIZED VIEW mv_empty AS SELECT id FROM t WITH NO DATA",
		"ALTER MATERIALIZED VIEW mv_empty SET ACCESS METHOD heap2",
	} {persistenceExec(t,p,b,c,q)}
}

func accessMethodControlCheck(run func(fixture.Step)) {
	for _,v:=range []struct{name,method string}{{"t","heap2"},{"ctas","heap3"},{"p","heap2"},{"p0","heap3"},{"attached","heap"},{"fallback",""},{"fallback0","heap3"},{"nested_root",""},{"nested_child",""},{"nested_leaf","heap3"},{"mv","heap3"},{"mv_empty","heap2"}} {run(accessMethodStep(v.name,v.method))}
	run(accessMethodRows([][]any{{int64(7),"keep"},{int64(8),nil}}))
	run(fixture.Select("SELECT id,note FROM ctas ORDER BY id",[]string{"id","note"},[]string{"INT4","TEXT"},[][]any{{int64(7),"keep"},{int64(8),nil}}))
	run(fixture.Select("SELECT id FROM nested_root",[]string{"id"},[]string{"INT4"},[][]any{{int64(51)}}))
	run(fixture.Select("SELECT id FROM p ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(11)}}))
	run(fixture.Select("SELECT id FROM fallback",[]string{"id"},[]string{"INT4"},[][]any{{int64(31)}}))
	run(fixture.Select("SELECT id FROM mv ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(7)},{int64(8)}}))
	fixture.MaterializedViewPopulation(run,"mv",true)
	fixture.MaterializedViewPopulation(run,"mv_empty",false)
}

func accessMethodControlRoundtrip(t *testing.T,p *fixture.Pair,check func(func(fixture.Step))) {
	t.Helper()
	var dump bytes.Buffer
	if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
	connector,err:=stuntgres.NewDriver().OpenConnector("access-method-restore")
	if err!=nil {t.Fatal(err)}
	target:=sql.OpenDB(connector);defer target.Close()
	c,err:=target.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer c.Close()
	for _,q:=range []string{
		"CREATE ACCESS METHOD heap3 TYPE TABLE HANDLER heap_tableam_handler",
		"SET default_table_access_method=heap3",
		"CREATE TEMP TABLE retained(id int)","ALTER TABLE retained SET ACCESS METHOD heap","INSERT INTO retained VALUES(99)",
		"CREATE TABLE sentinel(id int)","INSERT INTO sentinel VALUES(42)",
	} {if _,err=c.ExecContext(p.Ctx,q);err!=nil {t.Fatal(err)}}
	retained,err:=c.QueryContext(p.Ctx,"SELECT id FROM retained");if err!=nil {t.Fatal(err)};defer retained.Close()
	for range 2 {
		if err=stuntgres.Restore(p.Ctx,target,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)}
		check(func(step fixture.Step){persistenceControlRun(t,p,c,step)})
		persistenceControlRun(t,p,c,fixture.Select("SELECT id FROM retained",[]string{"id"},[]string{"INT4"},[][]any{{int64(99)}}))
		var again bytes.Buffer
		if err=stuntgres.Dump(p.Ctx,target,&again);err!=nil || !bytes.Equal(dump.Bytes(),again.Bytes()) {t.Fatal("noncanonical method roundtrip",err,dump.String(),again.String())}
	}
	fixture.StringCoreRetainedRows(t,retained,nil,[]string{"id"},[]string{"INT4"},[][]any{{int64(99)}},false)
	if _,err=c.ExecContext(p.Ctx,"CREATE TABLE sentinel(id int)");err!=nil {t.Fatal(err)}
	if _,err=c.ExecContext(p.Ctx,"INSERT INTO sentinel VALUES(42)");err!=nil {t.Fatal(err)}
	var before bytes.Buffer
	if err=stuntgres.Dump(p.Ctx,target,&before);err!=nil {t.Fatal(err)}
	readFailure:=errors.New("access method restore reader failure")
	if err=stuntgres.Restore(p.Ctx,target,addConstraintFailedReader{readFailure});!errors.Is(err,readFailure) {t.Fatal(err)}
	canceled,cancel:=context.WithCancel(p.Ctx);cancel()
	if err=stuntgres.Restore(canceled,target,bytes.NewReader(dump.Bytes()));!errors.Is(err,context.Canceled) {t.Fatal(err)}
	err=stuntgres.Restore(p.Ctx,target,strings.NewReader(dump.String()+"ALTER TABLE public.t SET ACCESS METHOD missing_am;"))
	want:=persistenceDiagnostic("42704",`access method "missing_am" does not exist`)
	fixture.AssertDiagnostic(t,err,&want)
	var after bytes.Buffer
	if err=stuntgres.Dump(p.Ctx,target,&after);err!=nil || !bytes.Equal(before.Bytes(),after.Bytes()) {t.Fatal("failed replay replaced committed state",err)}
	persistenceControlRun(t,p,c,fixture.Select("SELECT id FROM sentinel",[]string{"id"},[]string{"INT4"},[][]any{{int64(42)}}))
	check(func(step fixture.Step){persistenceControlRun(t,p,c,step)})
	// The source remains independent and its represented method identities hold.
	check(func(step fixture.Step){persistenceControlRun(t,p,p.Native,step)})
	if _,err=c.ExecContext(p.Ctx,"ALTER TABLE retained SET ACCESS METHOD heap3");err!=nil {t.Fatal(err)}
	err=stuntgres.Restore(p.Ctx,target,bytes.NewReader(dump.Bytes()))
	if err==nil || err.Error()!=`stuntgres: catalog replacement conflicts with live temporary access method "heap3"` {t.Fatal("same-name distinct provider boundary",err)}
	persistenceControlRun(t,p,c,accessMethodStep("retained","heap3"))
	persistenceControlRun(t,p,c,fixture.Select("SELECT id FROM retained",[]string{"id"},[]string{"INT4"},[][]any{{int64(99)}}))
	persistenceControlRun(t,p,c,fixture.Select("SELECT id FROM sentinel",[]string{"id"},[]string{"INT4"},[][]any{{int64(42)}}))
	check(func(step fixture.Step){persistenceControlRun(t,p,c,step)})
	check(func(step fixture.Step){persistenceControlRun(t,p,p.Native,step)})
	stmt,err:=c.PrepareContext(p.Ctx,"INSERT INTO t(id,note) VALUES($1,$2)");if err!=nil {t.Fatal(err)};defer stmt.Close()
	result,err:=stmt.ExecContext(p.Ctx,9,"after");if err!=nil {t.Fatal(err)}
	if count,err:=result.RowsAffected();err!=nil || count!=1 {t.Fatal(count,err)}
	persistenceControlRun(t,p,c,fixture.Select("SELECT note FROM t WHERE id=9",[]string{"note"},[]string{"TEXT"},[][]any{{"after"}}))
}

func TestReferenceAlterAccessMethodControls(t *testing.T) {
	accessMethodScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		accessMethodControlSetup(t,p,b,c)
		accessMethodControlCheck(func(step fixture.Step){persistenceRun(t,p,b,c,step)})
		persistenceRun(t,p,b,c,fixture.MaterializedViewUnpopulated("mv_empty"))
		if !b.PG {
			accessMethodControlRoundtrip(t,p,accessMethodControlCheck)
			if p.Postgres!=nil {
				// Native runs first. Cleanup must complete before PostgreSQL's
				// shared setup even when replay or an expectation fails.
				defer func() {
					ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second);defer cancel()
					for _,q:=range []string{"DROP MATERIALIZED VIEW IF EXISTS mv,mv_empty","DROP TABLE IF EXISTS t,ctas,p,fallback,nested_root CASCADE","DROP TYPE IF EXISTS pair","DROP ACCESS METHOD IF EXISTS heap2","DROP ACCESS METHOD IF EXISTS heap3"} {
						if _,err:=p.Postgres.ExecContext(ctx,q);err!=nil {t.Errorf("PostgreSQL replay cleanup: %v",err)}
					}
				}()
				var dump bytes.Buffer
				if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
				if _,err:=p.Postgres.ExecContext(p.Ctx,dump.String());err!=nil {t.Fatal("PostgreSQL dump replay",err)}
				for _,oracle:=range p.Backends() {if oracle.PG {
					run:=func(step fixture.Step){persistenceRun(t,p,oracle,p.Postgres,step)}
					// PostgreSQL executes CREATE WITH NO DATA, not the native
					// snapshot comments. Assert that boundary, then use its
					// ordinary REFRESH only for the originally populated MV.
					for _,name:=range []string{"mv","mv_empty"} {
						fixture.MaterializedViewPopulation(run,name,false)
						run(fixture.MaterializedViewUnpopulated(name))
					}
					run(fixture.Exec("REFRESH MATERIALIZED VIEW mv",0))
					accessMethodControlCheck(run)
					run(fixture.MaterializedViewUnpopulated("mv_empty"))
				}}
			}
		}
	})
}
