package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactAllTablesLifetime(t *testing.T) {
	fixture.SchemaReferenceCleanup(t,"xat_moved")
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		fixture.StatisticsExec(t,p,b,c,"CREATE SCHEMA xat_moved",0);tableStatCreate(t,p,b,c,"xat_lifetime")
		w,retire:=fixture.StatisticsReportingConn(t,p,b);defer retire();fixture.StatisticsExec(t,p,b,w,"BEGIN",0)
		fixture.StatisticsExec(t,p,b,w,"INSERT INTO xat_lifetime VALUES(1,NULL),(2,'b')",2)
		old:=xactTableGet(t,p,w,"xat_lifetime");xactTableCounts(t,old,2,0,0)
		fixture.StatisticsExec(t,p,b,w,"ALTER TABLE xat_lifetime RENAME TO xat_renamed",0)
		fixture.StatisticsExec(t,p,b,w,"ALTER TABLE xat_renamed SET SCHEMA xat_moved",0)
		moved:=xactTableGet(t,p,w,"xat_renamed");if moved.ID!=old.ID||moved.Schema!="xat_moved"{t.Fatal("rename/move reminted identity",moved,old)};xactTableCounts(t,moved,2,0,0)
		fixture.StatisticsExec(t,p,b,w,"ROLLBACK",0)
		restored:=xactTableGet(t,p,w,"xat_lifetime");if restored.ID!=old.ID||restored.Schema!="public"{t.Fatal("DDL rollback failed",restored,old)}
		holder:=fixture.IdentityConnection(t,p.Ctx,b.DB);held,err:=holder.QueryContext(p.Ctx,xactTableSelect,"xat_lifetime");if err!=nil{t.Fatal(err)};defer held.Close();columns,err:=held.ColumnTypes();if err!=nil{t.Fatal(err)}
		fixture.StatisticsExec(t,p,b,c,"DROP TABLE xat_lifetime",0)
		if !held.Next(){t.Fatal("retained result absent",held.Err())};var retained xactTableRow;if err:=held.Scan(retained.destinations()...);err!=nil{t.Fatal(err)};held.Close();xactTableMetadata(t,columns,b.PG)
		if retained.ID!=old.ID||retained.Name!="xat_lifetime"||retained.Schema!="public"{t.Fatal("retained identity changed",retained)};xactTableCounts(t,retained,0,0,0)
		var missing xactTableRow;if err:=c.QueryRowContext(p.Ctx,xactTableSelect,"xat_lifetime").Scan(missing.destinations()...);err!=sql.ErrNoRows{t.Fatal("drop did not remove membership",err)}
		tableStatCreate(t,p,b,c,"xat_lifetime");fresh:=xactTableGet(t,p,w,"xat_lifetime");if fresh.ID==old.ID||fresh.Seq!=0||fresh.Read!=0{t.Fatal("recreated identity inherited events",fresh,old)};xactTableCounts(t,fresh,0,0,0)
		if old.Name!="xat_lifetime"||old.Schema!="public"||old.Inserts!=2{t.Fatal("copied local row mutated",old)}
	})
}

func TestReferencePGStatXactAllTablesMaintenanceIsolation(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		tableStatCreate(t,p,b,c,"xat_maintenance");tableStatSeed(t,p,b,"xat_maintenance")
		fixture.StatisticsExec(t,p,b,c,"SET stats_fetch_consistency=none",0);fixture.StatisticsExec(t,p,b,c,"SET track_counts=off",0)
		before:=tableStatGet(t,p,c,"xat_maintenance")
		for range 3{
			xactTableGet(t,p,c,"xat_maintenance")
			stmt,err:=c.PrepareContext(p.Ctx,xactTableSelect);if err!=nil{t.Fatal(err)};var r xactTableRow;if err:=stmt.QueryRowContext(p.Ctx,"xat_maintenance").Scan(r.destinations()...);err!=nil{t.Fatal(err)};stmt.Close()
			if got:=tableStatGet(t,p,c,"xat_maintenance");!got.equal(before){t.Fatal("view reads advanced cumulative maintenance",got,before)}
		}
		fixture.StatisticsExec(t,p,b,c,"SET track_counts=on",0)
		fixture.StatisticsExec(t,p,b,c,"ANALYZE xat_maintenance",0)
		analyzed:=tableStatAwait(t,p,b,"xat_maintenance","completed ANALYZE1",func(r tableStatRow)bool{return r.Analyze==1})
		if !analyzed.LastAnalyze.Valid||analyzed.Vacuum!=0||analyzed.LastVacuum.Valid{t.Fatal("actual ANALYZE completion",analyzed)}
		fixture.StatisticsExec(t,p,b,c,"VACUUM xat_maintenance",0)
		vacuumed:=tableStatAwait(t,p,b,"xat_maintenance","completed VACUUM1",func(r tableStatRow)bool{return r.Vacuum==1})
		if !vacuumed.LastVacuum.Valid||vacuumed.Analyze!=1||!vacuumed.LastAnalyze.Time.Equal(analyzed.LastAnalyze.Time){t.Fatal("actual VACUUM completion",vacuumed)}
		for range 2{xactTableGet(t,p,c,"xat_maintenance");if got:=tableStatGet(t,p,c,"xat_maintenance");!got.equal(vacuumed){t.Fatal("statistics SELECT changed completion",got)}}
	})
}

func TestReferencePGStatXactAllTablesCancellation(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		tableStatCreate(t,p,b,c,"xat_cancel");before:=xactTableGet(t,p,c,"xat_cancel")
		ctx,cancel:=context.WithCancel(p.Ctx);cancel()
		rows,err:=b.DB.QueryContext(ctx,xactTableSelect,"xat_cancel");if rows!=nil{rows.Close()};if !errors.Is(err,context.Canceled){t.Fatal("pre-admission Query cancellation",err)};var state interface{SQLState()string};if errors.As(err,&state){t.Fatal("adapter error fabricated SQLSTATE",state.SQLState())}
		stmt,err:=b.DB.PrepareContext(ctx,xactTableSelect);if stmt!=nil{stmt.Close()};if !errors.Is(err,context.Canceled){t.Fatal("pre-admission Prepare cancellation",err)};if errors.As(err,&state){t.Fatal("adapter prepare fabricated SQLSTATE",state.SQLState())}
		if got:=xactTableGet(t,p,c,"xat_cancel");got!=before{t.Fatal("canceled view mutated state",got,before)}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM xat_cancel",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
	})
}

func TestReferencePGStatXactAllTablesNativeControls(t *testing.T) {
	var emitted []byte
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		if b.PG{
			if len(emitted)==0{t.Fatal("missing actual native dump")};if _,err:=c.ExecContext(p.Ctx,string(emitted));err!=nil{t.Fatal("oracle replay",err)}
			fixture.StatisticsExec(t,p,b,c,"BEGIN",0);xactTableEnd(t,p,b,c,"COMMIT")
			xactTableCounts(t,xactTableGet(t,p,c,"xat_control"),0,0,0)
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT n,payload FROM xat_control ORDER BY n",[]string{"n","payload"},[]string{"INT4","TEXT"},[][]any{{int64(1),"a"},{int64(2),nil},{int64(3),"c"}}));return
		}
		tableStatCreate(t,p,b,c,"xat_control");tableStatSeed(t,p,b,"xat_control")
		fixture.StatisticsExec(t,p,b,c,"SET stats_fetch_consistency=none",0)
		for range 2{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT payload FROM xat_control WHERE n=2",[]string{"payload"},[]string{"TEXT"},[][]any{{nil}}))}
		fixture.StatisticsExec(t,p,b,c,"SET track_counts=off",0)
		// A single admitted query sums the two authorities, so an ordinary
		// eligible publication between control calls cannot look like lost work.
		totalSQL:="SELECT x.seq_scan+s.seq_scan AS scans,x.seq_tup_read+s.seq_tup_read AS tuples FROM pg_stat_xact_all_tables x JOIN pg_stat_all_tables s ON s.relid=x.relid WHERE x.relname='xat_control'"
		total:=func(scans,tuples int64){t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select(totalSQL,[]string{"scans","tuples"},[]string{"INT8","INT8"},[][]any{{scans,tuples}}))}
		total(2,6)
		var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)};emitted=bytes.Clone(dump.Bytes())
		if bytes.Contains(emitted,[]byte("pg_stat_xact_all_tables")){t.Fatal("dump serialized monitoring history")}
		keeper:=fixture.IdentityConnection(t,p.Ctx,b.DB)
		fixture.StatisticsExec(t,p,b,keeper,"CREATE TEMP TABLE xat_temp(n integer,payload text)",0)
		// An explicit owner retains positive local read work until terminal
		// settlement; replacement must never transplant that old-epoch batch.
		fixture.StatisticsExec(t,p,b,keeper,"BEGIN",0)
		fixture.StatisticsExec(t,p,b,keeper,"INSERT INTO xat_temp VALUES(1,NULL)",1)
		fixture.StatisticsExec(t,p,b,keeper,"COMMIT",0)
		fixture.StatisticsExec(t,p,b,keeper,"BEGIN",0)
		fixture.RunStep(t,p.Ctx,keeper,b.Recorder,fixture.Select("SELECT n FROM xat_temp",[]string{"n"},[]string{"INT4"},[][]any{{int64(1)}}))
		pending:=xactTableGet(t,p,keeper,"xat_temp");if pending.Seq!=1||pending.Read!=1{t.Fatal("positive temporary local batch",pending)}
		fixture.StatisticsExec(t,p,b,keeper,"ROLLBACK",0)
		old:=xactTableGet(t,p,c,"xat_control")
		unchanged:=func(){t.Helper();if got:=xactTableGet(t,p,c,"xat_control");got.ID!=old.ID{t.Fatal("failed Restore changed identity",got,old)};total(2,6);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM xat_control",[]string{"count"},[]string{"INT8"},[][]any{{int64(3)}}));if got:=xactTableGet(t,p,keeper,"xat_temp");got.ID!=pending.ID{t.Fatal("failed Restore changed attached TEMP",got,pending)}}
		bad:=append(bytes.Clone(emitted),[]byte("\nCREATE TABLE xat_bad(n no_such_type);")...);if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(bad));err==nil{t.Fatal("late invalid Restore accepted")};unchanged()
		ctx,cancel:=context.WithCancel(p.Ctx);cancel();if err:=stuntgres.Restore(ctx,p.Native,bytes.NewReader(emitted));!errors.Is(err,context.Canceled){t.Fatal(err)};unchanged()
		during,cancelInput:=context.WithCancel(p.Ctx);input:=&indexStatCancelReader{Reader:bytes.NewReader(emitted),cancel:cancelInput};err:=stuntgres.Restore(during,p.Native,input);cancelInput();if !errors.Is(err,context.Canceled)||!input.entered{t.Fatal("admitted input cancellation",input.entered,err)};unchanged()
		if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(emitted));err!=nil{t.Fatal(err)}
		fresh:=xactTableGet(t,p,c,"xat_control");xactTableCounts(t,fresh,0,0,0);if fresh.Seq!=0||fresh.Read!=0{t.Fatal("persistent pending crossed replacement",fresh)}
		total(0,0)
		temp:=xactTableGet(t,p,keeper,"xat_temp");if temp.ID!=pending.ID||temp.Seq!=0||temp.Read!=0{t.Fatal("TEMP pending crossed epoch",temp,pending)};xactTableCounts(t,temp,0,0,0)
		if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
		var absent xactTableRow;if err:=c.QueryRowContext(p.Ctx,xactTableSelect,"xat_control").Scan(absent.destinations()...);err!=sql.ErrNoRows{t.Fatal("Reset persistent membership",err)}
		if got:=xactTableGet(t,p,keeper,"xat_temp");got.ID!=temp.ID||got.Seq!=0{t.Fatal("Reset TEMP membership/local history",got,temp)}
	})
}
