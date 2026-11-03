package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGStatioUserTablesLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t,"statio_user_moved")
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
		a,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
		statioSequenceExec(t,p,b,a,"CREATE SCHEMA statio_user_moved")
		statioSequenceExec(t,p,b,a,"CREATE TABLE statio_user_lifecycle(k integer)")
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_user_lifecycle VALUES(1),(2)",2))
		oid:=statioSequenceOID(t,p,a,"statio_user_lifecycle")
		statioTableWork(t,p,b,nil,"SELECT k FROM statio_user_lifecycle ORDER BY k",[][]any{{int64(1)},{int64(2)}})
		stmt,err:=observer.PrepareContext(p.Ctx,statioUserTableProbe);if err!=nil {t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:observer,Statement:stmt,SQL:statioUserTableProbe}
		before:=statioUserTableOne(t,p,b,held,oid);statioTableHeap(t,b,before,1)
		statioTableAbsentToast(t,before)
		// Pre-admission context errors are a native client-boundary contract.
		// A fast pq query can finish before cancellation; do not interrupt or
		// reuse its physical owner with a timing-dependent pre-canceled probe.
		if !b.PG {
			canceled,cancel:=context.WithCancel(p.Ctx);cancel()
			rows,err:=stmt.QueryContext(canceled,oid)
			if rows!=nil {rows.Close();t.Fatal("pre-canceled native query returned rows")}
			fixture.DriverOrdinaryError(t,err,context.Canceled)
		}
		want:=statioTableWriteDiagnostic("update")
		_,err=a.ExecContext(p.Ctx,"UPDATE pg_statio_user_tables SET heap_blks_hit=0 WHERE false");fixture.AssertDiagnostic(t,err,&want)
		if got:=statioUserTableOne(t,p,b,held,oid);!b.PG && got!=before {t.Fatal("rejected/canceled read changed facts",before,got)}
		var old *sql.Rows
		if !b.PG {old,err=observer.QueryContext(p.Ctx,statioUserTableProbe,oid);if err!=nil {t.Fatal(err)};defer old.Close();statioTableMetadata(t,old,false)}
		// A real private source Open must not resurrect an aborted identity
		// when its physical writer eventually publishes the completed work.
		writer,retire:=fixture.StatisticsReportingConn(t,p,b)
		for _,q:=range []string{"BEGIN","CREATE TABLE statio_user_aborted(k integer)"} {statioSequenceExec(t,p,b,writer,q)}
		fixture.RunStep(t,p.Ctx,writer,b.Recorder,fixture.Exec("INSERT INTO statio_user_aborted VALUES(7)",1))
		fixture.RunStep(t,p.Ctx,writer,b.Recorder,fixture.Select("SELECT k FROM statio_user_aborted",[]string{"k"},[]string{"INT4"},[][]any{{int64(7)}}))
		aborted:=statioSequenceOID(t,p,writer,"statio_user_aborted")
		statioUserTableOne(t,p,b,writer,aborted)
		if rows:=statioTableRows(t,p,b,held,statioUserTableProbe,aborted);len(rows)!=0 {t.Fatal("private creation leaked",rows)}
		statioSequenceExec(t,p,b,writer,"ROLLBACK")
		retire()
		if rows:=statioTableRows(t,p,b,held,statioUserTableProbe,aborted);len(rows)!=0 {t.Fatal("aborted creation resurrected",rows)}
		statioSequenceExec(t,p,b,a,"CREATE TABLE statio_user_aborted(k integer)")
		reborn:=statioSequenceOID(t,p,a,"statio_user_aborted");if reborn==aborted {t.Fatal("aborted identity reused")}
		statioTableHeap(t,b,statioUserTableOne(t,p,b,held,reborn),0)
		statioSequenceExec(t,p,b,a,"DROP TABLE statio_user_aborted")
		for _,q:=range []string{"ALTER TABLE statio_user_lifecycle RENAME TO statio_user_renamed","ALTER TABLE statio_user_renamed SET SCHEMA statio_user_moved"} {statioSequenceExec(t,p,b,a,q)}
		r:=statioUserTableOne(t,p,b,held,oid)
		if r.schema!="statio_user_moved" || r.name!="statio_user_renamed" {t.Fatal("held statement recaptures names",r)}
		statioTableAbsentToast(t,r)
		statioTableHeap(t,b,r,1)
		for _,q:=range []string{"BEGIN","DROP TABLE statio_user_moved.statio_user_renamed","ROLLBACK"} {statioSequenceExec(t,p,b,a,q)}
		statioUserTableOne(t,p,b,held,oid)
		statioSequenceExec(t,p,b,a,"DROP TABLE statio_user_moved.statio_user_renamed")
		if rows:=statioTableRows(t,p,b,held,statioUserTableProbe,oid);len(rows)!=0 {t.Fatal("dropped identity",rows)}
		for _,q:=range []string{statioTableProbe,"SELECT * FROM pg_statio_sys_tables WHERE relid=$1::oid"} {if rows:=statioTableRows(t,p,b,observer,q,oid);len(rows)!=0 {t.Fatal("dropped companion identity",q,rows)}}
		if old!=nil {statioTableRetained(t,old,before)}
		statioSequenceExec(t,p,b,a,"CREATE TABLE statio_user_moved.statio_user_renamed(k integer)")
		next:=statioSequenceOID(t,p,a,"statio_user_moved.statio_user_renamed");if next==oid {t.Fatal("retired OID reused")}
		statioTableHeap(t,b,statioUserTableOne(t,p,b,held,next),0)
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_user_moved.statio_user_renamed VALUES(1),(2)",2))
		statioTableWork(t,p,b,nil,"SELECT k FROM statio_user_moved.statio_user_renamed ORDER BY k",[][]any{{int64(1)},{int64(2)}})
		if !b.PG {statioUserTableControls(t,p,b,a,observer,held,next)}
	})}
}

func statioUserTableControls(t *testing.T,p *fixture.Pair,b fixture.Backend,a,observer *sql.Conn,held fixture.Executor,oid int64) {
	t.Helper()
	before:=statioUserTableOne(t,p,b,observer,oid);statioTableHeap(t,b,before,1)
	statioTableAbsentToast(t,before)
	old,err:=observer.QueryContext(p.Ctx,statioUserTableProbe,oid);if err!=nil {t.Fatal(err)};defer old.Close();statioTableMetadata(t,old,false)
	var dump bytes.Buffer
	if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
	if strings.Contains(dump.String(),"pg_statio_user_tables") || strings.Contains(dump.String(),"heap_blks_hit") {t.Fatal("dump serialized monitoring history",dump.String())}
	fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_user_moved.statio_user_renamed VALUES(3)",1))
	// A real late invalid statement must not publish the staged replacement.
	want:=fixture.ControlsReferenceDiagnostic("22023","CACHE (0) must be greater than zero")
	fixture.AssertDiagnostic(t,stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump.String()+"CREATE SEQUENCE statio_user_bad CACHE 0;")),&want)
	if got:=statioUserTableOne(t,p,b,observer,oid);got!=before {t.Fatal("failed Restore changed provider/catalog",got,before)}
	fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT k FROM statio_user_moved.statio_user_renamed ORDER BY k",[]string{"k"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)},{int64(3)}}))
	if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
	if got:=statioTableRows(t,p,b,observer,statioUserTableProbe,oid);len(got)!=0 {t.Fatal("Reset retained user identity",got)}
	if got:=statioTableRows(t,p,b,held,statioUserTableProbe,oid);len(got)!=0 {t.Fatal("held Reset retained identity",got)}
	statioTableRows(t,p,b,a,"SELECT * FROM pg_statio_user_tables WHERE false")
	for _,q:=range []string{statioTableProbe,"SELECT * FROM pg_statio_sys_tables WHERE relid=$1::oid"} {if rows:=statioTableRows(t,p,b,observer,q,oid);len(rows)!=0 {t.Fatal("Reset companion identity",q,rows)}}
	if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump.String()));err!=nil {t.Fatal(err)}
	restored:=statioSequenceOID(t,p,a,"statio_user_moved.statio_user_renamed")
	restoredRow:=statioUserTableOne(t,p,b,observer,restored)
	statioTableHeap(t,b,restoredRow,0);statioTableAbsentToast(t,restoredRow)
	if restored==oid {t.Fatal("Restore reused retired identity")}
	statioUserTableCompanions(t,p,b,observer,restored)
	if got:=statioUserTableOne(t,p,b,held,restored);got!=restoredRow {t.Fatal("held execution did not recapture restored identity",got,restoredRow)}
	fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT k FROM statio_user_moved.statio_user_renamed ORDER BY k",[]string{"k"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}}))
	statioTableRetained(t,old,before)
}
