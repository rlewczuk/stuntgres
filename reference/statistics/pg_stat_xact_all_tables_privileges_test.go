package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactAllTablesMembershipAndPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"xat_reader"});fixture.SchemaReferenceCleanup(t,"xat_private")
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		defer func(){ctx,cancel:=context.WithTimeout(context.Background(),fixture.Liveness);defer cancel();if _,err:=c.ExecContext(ctx,"DROP SERVER IF EXISTS xat_server CASCADE");err!=nil{t.Error(err)};if _,err:=c.ExecContext(ctx,"DROP FOREIGN DATA WRAPPER IF EXISTS xat_fdw");err!=nil{t.Error(err)}}()
		tableStatCreate(t,p,b,c,"xat_members")
		for _,q:=range []string{"CREATE VIEW xat_view AS SELECT n FROM xat_members","CREATE SEQUENCE xat_sequence","CREATE INDEX xat_index ON xat_members(n)","CREATE MATERIALIZED VIEW xat_mat AS SELECT n FROM xat_members","CREATE TABLE xat_partition(n integer) PARTITION BY RANGE(n)","CREATE TABLE xat_leaf PARTITION OF xat_partition FOR VALUES FROM(0) TO(10)","CREATE FOREIGN DATA WRAPPER xat_fdw","CREATE SERVER xat_server FOREIGN DATA WRAPPER xat_fdw","CREATE FOREIGN TABLE xat_foreign(n integer) SERVER xat_server"}{fixture.StatisticsExec(t,p,b,c,q,0)}
		for _,tc:=range []struct{name string;count int64}{{"xat_members",1},{"xat_mat",1},{"xat_partition",1},{"xat_leaf",1},{"xat_view",0},{"xat_sequence",0},{"xat_index",0},{"xat_foreign",0}}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM pg_stat_xact_all_tables WHERE relname=$1",[]string{"count"},[]string{"INT8"},[][]any{{tc.count}},tc.name))}
		var toast int64;if err:=c.QueryRowContext(p.Ctx,"SELECT reltoastrelid FROM pg_class WHERE oid='xat_members'::regclass").Scan(&toast);err!=nil{t.Fatal(err)};if toast>0{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM pg_stat_xact_all_tables WHERE relid=$1",[]string{"count"},[]string{"INT8"},[][]any{{int64(1)}},toast))}
		fixture.StatisticsExec(t,p,b,c,"CREATE ROLE xat_reader LOGIN PASSWORD 'role-attributes-fixture'",0)
		fixture.StatisticsExec(t,p,b,c,"CREATE SCHEMA xat_private",0);tableStatCreate(t,p,b,c,"xat_private.xat_secret")
		pool:=fixture.RoleAttributesPool(t,p,b,"xat_reader");reader,err:=pool.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer reader.Close()
		fixture.RunStep(t,p.Ctx,reader,b.Recorder,fixture.ErrorDiagnostic("SELECT payload FROM xat_private.xat_secret",fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42501",Message:"permission denied for schema xat_private",Position:"21"}))
		initial:=xactTableGet(t,p,reader,"xat_secret");if initial.Schema!="xat_private"||initial.ID<=0||initial.Index.Valid||initial.Fetch.Valid{t.Fatal("identity redaction or absent-index fabrication",initial)}
		w,retire:=fixture.StatisticsReportingConn(t,p,b)
		fixture.StatisticsExec(t,p,b,w,"BEGIN",0);fixture.StatisticsExec(t,p,b,w,"INSERT INTO xat_private.xat_secret VALUES(1,NULL),(2,'b')",2)
		xactTableCounts(t,xactTableGet(t,p,w,"xat_secret"),2,0,0);xactTableCounts(t,xactTableGet(t,p,reader,"xat_secret"),0,0,0)
		xactTableEnd(t,p,b,w,"ROLLBACK");retire()
		// INSERT privilege does not require table SELECT. Schema visibility and
		// base table SELECT remain separate from the monitoring view's ACL.
		fixture.StatisticsExec(t,p,b,c,"GRANT USAGE ON SCHEMA xat_private TO xat_reader",0)
		fixture.StatisticsExec(t,p,b,c,"GRANT INSERT ON xat_private.xat_secret TO xat_reader",0)
		fixture.StatisticsExec(t,p,b,reader,"BEGIN",0);fixture.StatisticsExec(t,p,b,reader,"INSERT INTO xat_private.xat_secret VALUES(3,NULL)",1)
		xactTableCounts(t,xactTableGet(t,p,reader,"xat_secret"),1,0,0);xactTableEnd(t,p,b,reader,"ROLLBACK")
		stmt,err:=reader.PrepareContext(p.Ctx,xactTableSelect);if err!=nil{t.Fatal(err)};defer stmt.Close()
		falseSQL:="SELECT * FROM pg_catalog.pg_stat_xact_all_tables WHERE FALSE";falseStmt,err:=reader.PrepareContext(p.Ctx,falseSQL);if err!=nil{t.Fatal(err)};defer falseStmt.Close()
		defer c.ExecContext(p.Ctx,"GRANT SELECT ON pg_catalog.pg_stat_xact_all_tables TO PUBLIC")
		fixture.StatisticsExec(t,p,b,c,"REVOKE SELECT ON pg_catalog.pg_stat_xact_all_tables FROM PUBLIC",0)
		denial:=fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42501",Message:"permission denied for view pg_stat_xact_all_tables"}
		rows,err:=stmt.QueryContext(p.Ctx,"xat_secret");if rows!=nil{rows.Close()};fixture.AssertDiagnostic(t,err,&denial)
		held:=fixture.HeldStatement{Executor:reader,Statement:falseStmt,SQL:falseSQL};step:=fixture.ErrorDiagnostic(falseSQL,denial);step.ErrorStage="execute";fixture.RunStep(t,p.Ctx,held,b.Recorder,step)
		for _,prepared:=range []bool{false,true}{step:=fixture.ErrorDiagnostic(falseSQL,denial);step.Prepared=prepared;step.ErrorStage="execute";fixture.RunStep(t,p.Ctx,reader,b.Recorder,step)}
		fixture.StatisticsExec(t,p,b,c,"GRANT SELECT ON pg_catalog.pg_stat_xact_all_tables TO PUBLIC",0)
		var restored xactTableRow;if err:=stmt.QueryRowContext(p.Ctx,"xat_secret").Scan(restored.destinations()...);err!=nil||restored.ID!=initial.ID{t.Fatal("held SELECT grant recovery",restored,err)}
	})
}
