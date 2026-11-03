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

func TestReferencePGStatioAllTablesLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t,"statio_table_moved")
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
		a,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
		statioSequenceExec(t,p,b,a,"CREATE SCHEMA statio_table_moved")
		statioSequenceExec(t,p,b,a,"CREATE TABLE statio_table_lifecycle(k integer)")
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_table_lifecycle VALUES(1),(2)",2))
		oid:=statioSequenceOID(t,p,a,"statio_table_lifecycle")
		statioTableWork(t,p,b,nil,"SELECT k FROM statio_table_lifecycle ORDER BY k",[][]any{{int64(1)},{int64(2)}})
		stmt,err:=observer.PrepareContext(p.Ctx,statioTableProbe);if err!=nil {t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:observer,Statement:stmt,SQL:statioTableProbe}
		before:=statioTableOne(t,p,b,held,oid);statioTableHeap(t,b,before,1)
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
		_,err=a.ExecContext(p.Ctx,"UPDATE pg_statio_all_tables SET heap_blks_hit=0 WHERE false");fixture.AssertDiagnostic(t,err,&want)
		if got:=statioTableOne(t,p,b,held,oid);!b.PG && got!=before {t.Fatal("rejected/canceled read changed facts",before,got)}
		var old *sql.Rows
		if !b.PG {old,err=observer.QueryContext(p.Ctx,statioTableProbe,oid);if err!=nil {t.Fatal(err)};defer old.Close()}
		for _,q:=range []string{"ALTER TABLE statio_table_lifecycle RENAME TO statio_table_renamed","ALTER TABLE statio_table_renamed SET SCHEMA statio_table_moved"} {statioSequenceExec(t,p,b,a,q)}
		r:=statioTableOne(t,p,b,held,oid)
		if r.schema!="statio_table_moved" || r.name!="statio_table_renamed" {t.Fatal("held statement recaptures names",r)}
		statioTableAbsentToast(t,r)
		statioTableHeap(t,b,r,1)
		for _,q:=range []string{"BEGIN","DROP TABLE statio_table_moved.statio_table_renamed","ROLLBACK"} {statioSequenceExec(t,p,b,a,q)}
		statioTableOne(t,p,b,held,oid)
		statioSequenceExec(t,p,b,a,"DROP TABLE statio_table_moved.statio_table_renamed")
		if rows:=statioTableRows(t,p,b,held,statioTableProbe,oid);len(rows)!=0 {t.Fatal("dropped identity",rows)}
		if old!=nil {statioTableRetained(t,old,before)}
		statioSequenceExec(t,p,b,a,"CREATE TABLE statio_table_moved.statio_table_renamed(k integer)")
		next:=statioSequenceOID(t,p,a,"statio_table_moved.statio_table_renamed");if next==oid {t.Fatal("retired OID reused")}
		statioTableHeap(t,b,statioTableOne(t,p,b,held,next),0)
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_table_moved.statio_table_renamed VALUES(1),(2)",2))
		statioTableWork(t,p,b,nil,"SELECT k FROM statio_table_moved.statio_table_renamed ORDER BY k",[][]any{{int64(1)},{int64(2)}})
		if !b.PG {statioTableControls(t,p,b,a,observer,next)}
	})}
}

func statioTableRetained(t *testing.T,rows *sql.Rows,want statioTableRow) {
	t.Helper();defer rows.Close();statioTableMetadata(t,rows,false)
	if !rows.Next() {t.Fatal("retained row missing",rows.Err())}
	if got:=statioTableScan(t,rows,false);got!=want {t.Fatal("old row mutated",got,want)}
	if rows.Next() || rows.Err()!=nil {t.Fatal("retained cardinality",rows.Err())}
}

func statioTableControls(t *testing.T,p *fixture.Pair,b fixture.Backend,a,observer *sql.Conn,oid int64) {
	t.Helper()
	before:=statioTableOne(t,p,b,observer,oid);statioTableHeap(t,b,before,1)
	statioTableAbsentToast(t,before)
	old,err:=observer.QueryContext(p.Ctx,statioTableProbe,oid);if err!=nil {t.Fatal(err)};defer old.Close()
	var dump bytes.Buffer
	if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
	if strings.Contains(dump.String(),"pg_statio_all_tables") || strings.Contains(dump.String(),"heap_blks_hit") {t.Fatal("dump serialized monitoring history",dump.String())}
	fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_table_moved.statio_table_renamed VALUES(3)",1))
	// A real late invalid statement must not publish the staged replacement.
	want:=fixture.ControlsReferenceDiagnostic("22023","CACHE (0) must be greater than zero")
	fixture.AssertDiagnostic(t,stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump.String()+"CREATE SEQUENCE statio_table_bad CACHE 0;")),&want)
	if got:=statioTableOne(t,p,b,observer,oid);got!=before {t.Fatal("failed Restore changed provider/catalog",got,before)}
	fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT k FROM statio_table_moved.statio_table_renamed ORDER BY k",[]string{"k"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)},{int64(3)}}))
	if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
	if got:=statioTableRows(t,p,b,observer,statioTableProbe,oid);len(got)!=0 {t.Fatal("Reset retained user identity",got)}
	statioTableRows(t,p,b,a,"SELECT * FROM pg_statio_all_tables WHERE false")
	if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump.String()));err!=nil {t.Fatal(err)}
	restored:=statioSequenceOID(t,p,a,"statio_table_moved.statio_table_renamed")
	restoredRow:=statioTableOne(t,p,b,observer,restored)
	statioTableHeap(t,b,restoredRow,0);statioTableAbsentToast(t,restoredRow)
	fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT k FROM statio_table_moved.statio_table_renamed ORDER BY k",[]string{"k"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}}))
	statioTableRetained(t,old,before)
}
