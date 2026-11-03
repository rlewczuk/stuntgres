package reference_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGStatioSysTablesLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t,"statio_sys_table_moved")
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
		a,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
		for _,q:=range []string{"CREATE SCHEMA statio_sys_table_moved","CREATE TABLE statio_sys_lifecycle(k integer)"} {statioSequenceExec(t,p,b,a,q)}
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_sys_lifecycle VALUES(1),(2)",2))
		oid:=statioSequenceOID(t,p,a,"statio_sys_lifecycle")
		held,stmt:=statioSysTableHeld(t,p,observer);defer stmt.Close()
		allStmt,err:=observer.PrepareContext(p.Ctx,statioTableProbe);if err!=nil {t.Fatal(err)};defer allStmt.Close()
		allHeld:=fixture.HeldStatement{Executor:observer,Statement:allStmt,SQL:statioTableProbe}
		excluded:=func(id int64){t.Helper();for _,e:=range []fixture.Executor{observer,held} {statioSysTableEmpty(t,p,b,e,id)}}
		excluded(oid);statioSysTablePositive(t,p,b,held,1259)
		statioTableWork(t,p,b,nil,"SELECT k FROM statio_sys_lifecycle ORDER BY k",[][]any{{int64(1)},{int64(2)}})
		before:=statioTableOne(t,p,b,allHeld,oid);statioTableHeap(t,b,before,1);statioTableAbsentToast(t,before);excluded(oid)
		if !b.PG {ctx,cancel:=context.WithCancel(p.Ctx);cancel();rows,err:=stmt.QueryContext(ctx,1259);if rows!=nil {rows.Close();t.Fatal("canceled query admitted rows")};fixture.DriverOrdinaryError(t,err,context.Canceled);if fresh:=statioTableOne(t,p,b,allHeld,oid);fresh!=before {t.Fatal("canceled read changed facts",fresh,before)}}
		for _,q:=range []string{"ALTER TABLE statio_sys_lifecycle RENAME TO statio_sys_renamed","ALTER TABLE statio_sys_renamed SET SCHEMA statio_sys_table_moved"} {statioSequenceExec(t,p,b,a,q);excluded(oid)}
		moved:=statioTableOne(t,p,b,allHeld,oid);if moved.schema!="statio_sys_table_moved" || moved.name!="statio_sys_renamed" {t.Fatal("current names through held binding",moved)};statioTableHeap(t,b,moved,1)
		statioSequenceExec(t,p,b,a,"DROP TABLE statio_sys_table_moved.statio_sys_renamed")
		excluded(oid);if rows:=statioTableRows(t,p,b,allHeld,statioTableProbe,oid);len(rows)!=0 {t.Fatal("dropped ALL identity",rows)}
		statioSequenceExec(t,p,b,a,"CREATE TABLE statio_sys_table_moved.statio_sys_renamed(k integer)")
		next:=statioSequenceOID(t,p,a,"statio_sys_table_moved.statio_sys_renamed");if next==oid {t.Fatal("recreated identity reused")}
		statioTableHeap(t,b,statioTableOne(t,p,b,allHeld,next),0);excluded(next)
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_sys_table_moved.statio_sys_renamed VALUES(1),(2)",2))
		if !b.PG {
			positive:=statioSysTablePositive(t,p,b,held,1259)
			old,err:=observer.QueryContext(p.Ctx,statioSysTableProbe,int64(1259));if err!=nil {t.Fatal(err)};defer old.Close()
			// Retain an admitted eager Rows and its descriptors, not merely
			// copied values after consumption, across catalog replacement.
			statioTableMetadata(t,old,false)
			var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
			if strings.Contains(dump.String(),"pg_statio_sys_tables") || strings.Contains(dump.String(),"heap_blks_hit") {t.Fatal("monitoring serialization",dump.String())}
			userBefore:=statioTableOne(t,p,b,allHeld,next)
			fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_sys_table_moved.statio_sys_renamed VALUES(3)",1))
			want:=fixture.ControlsReferenceDiagnostic("22023","CACHE (0) must be greater than zero")
			fixture.AssertDiagnostic(t,stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump.String()+"CREATE SEQUENCE statio_sys_bad CACHE 0;")),&want)
			if fresh:=statioTableOne(t,p,b,allHeld,next);fresh!=userBefore {t.Fatal("failed restore changed current history",fresh,userBefore)}
			if fresh:=statioSysTablePositive(t,p,b,held,1259);fresh!=positive {t.Fatal("failed restore changed selected view",fresh,positive)}
			fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT k FROM statio_sys_table_moved.statio_sys_renamed ORDER BY k",[]string{"k"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)},{int64(3)}}))
			if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
			excluded(next);if rows:=statioTableRows(t,p,b,allHeld,statioTableProbe,next);len(rows)!=0 {t.Fatal("reset old identity",rows)}
			statioSysTablePositive(t,p,b,held,1259)
			if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump.String()));err!=nil {t.Fatal(err)}
			restored:=statioSequenceOID(t,p,a,"statio_sys_table_moved.statio_sys_renamed")
			excluded(restored);statioTableHeap(t,b,statioTableOne(t,p,b,allHeld,restored),0)
			statioSysTablePositive(t,p,b,held,1259)
			fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT k FROM statio_sys_table_moved.statio_sys_renamed ORDER BY k",[]string{"k"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}}))
			statioTableRetained(t,old,positive)
		}
		statioSysTablePositive(t,p,b,held,1259)
	})}
}
