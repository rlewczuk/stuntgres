package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactAllTablesTransactions(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for _,end:=range []string{"ROLLBACK","COMMIT"}{
			name:="xat_abort";if end=="COMMIT"{name="xat_commit"};tableStatCreate(t,p,b,c,name)
			w,retire:=fixture.StatisticsReportingConn(t,p,b)
			fixture.StatisticsExec(t,p,b,w,"BEGIN",0)
			stmt,err:=w.PrepareContext(p.Ctx,xactTableSelect);if err!=nil{t.Fatal(err)}
			xactTableCounts(t,xactTableGet(t,p,w,name),0,0,0)
			fixture.StatisticsExec(t,p,b,w,"INSERT INTO "+name+" VALUES(1,'a'),(2,NULL)",2)
			xactTableCounts(t,xactTableGet(t,p,w,name),2,0,0)
			var held xactTableRow;if err:=stmt.QueryRowContext(p.Ctx,name).Scan(held.destinations()...);err!=nil{t.Fatal(err)};xactTableCounts(t,held,2,0,0);stmt.Close()
			xactTableCounts(t,xactTableGet(t,p,c,name),0,0,0)
			xactTableEnd(t,p,b,w,end)
			fixture.StatisticsExec(t,p,b,w,"BEGIN",0);xactTableCounts(t,xactTableGet(t,p,w,name),0,0,0);fixture.StatisticsExec(t,p,b,w,"ROLLBACK",0);retire()
			published:=tableStatAwait(t,p,b,name,"executed inserts2",func(r tableStatRow)bool{return r.Inserts==2})
			if published.Updates!=0||published.Deletes!=0{t.Fatal("cumulative executed tuple vector",published)}
			count:=int64(0);if end=="COMMIT"{count=2}
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM "+name,[]string{"count"},[]string{"INT8"},[][]any{{count}}))
		}
	})
}

func TestReferencePGStatXactAllTablesSavepoints(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		tableStatCreate(t,p,b,c,"xat_frames")
		w,retire:=fixture.StatisticsReportingConn(t,p,b);defer retire()
		fixture.StatisticsExec(t,p,b,w,"BEGIN",0)
		fixture.StatisticsExec(t,p,b,w,"INSERT INTO xat_frames VALUES(1,'a'),(2,NULL)",2)
		fixture.StatisticsExec(t,p,b,w,"SAVEPOINT child",0)
		fixture.StatisticsExec(t,p,b,w,"INSERT INTO xat_frames VALUES(3,NULL)",1)
		xactTableCounts(t,xactTableGet(t,p,w,"xat_frames"),3,0,0)
		fixture.StatisticsExec(t,p,b,w,"ROLLBACK TO child",0)
		xactTableCounts(t,xactTableGet(t,p,w,"xat_frames"),3,0,0)
		fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT count(*) FROM xat_frames",[]string{"count"},[]string{"INT8"},[][]any{{int64(2)}}))
		fixture.StatisticsExec(t,p,b,w,"INSERT INTO xat_frames VALUES(4,NULL)",1)
		fixture.StatisticsExec(t,p,b,w,"RELEASE child",0)
		xactTableCounts(t,xactTableGet(t,p,w,"xat_frames"),4,0,0)
		fixture.StatisticsExec(t,p,b,w,"UPDATE xat_frames SET payload=NULL WHERE n=1",1)
		fixture.StatisticsExec(t,p,b,w,"DELETE FROM xat_frames WHERE n=1",1)
		updated:=xactTableGet(t,p,w,"xat_frames");xactTableCounts(t,updated,4,1,1)
		if !b.PG&&(updated.HOT!=0||updated.Newpage!=0){t.Fatal("native physical placement estimates",updated)}
		xactTableEnd(t,p,b,w,"ROLLBACK")
		fixture.StatisticsExec(t,p,b,c,"CREATE TABLE xat_atomic(n integer NOT NULL)",0)
		fixture.StatisticsExec(t,p,b,w,"BEGIN",0);fixture.StatisticsExec(t,p,b,w,"SAVEPOINT failed",0)
		// Native's ordinary VALUES path validates the whole batch before
		// StageInsert: this failure executes no physical insertion. PostgreSQL
		// has already inserted the accepted prefix. Neither reports committed rows.
		step:=fixture.ImplicitError("INSERT INTO xat_atomic VALUES(1),(NULL)",fixture.Diagnostic{Code:"23502",Message:`null value in column "n" of relation "xat_atomic" violates not-null constraint`,Detail:"Failing row contains (null).",Schema:"public",Table:"xat_atomic",Column:"n"});step.ErrorStage="execute";fixture.RunStep(t,p.Ctx,w,b.Recorder,step)
		fixture.StatisticsExec(t,p,b,w,"ROLLBACK TO failed",0)
		plainInserts:=int64(0);if b.PG{plainInserts=1}
		xactTableCounts(t,xactTableGet(t,p,w,"xat_atomic"),plainInserts,0,0)
		fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT count(*) FROM xat_atomic",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
		// RETURNING uses the existing incremental accepted-row seam on both
		// backends. The first insertion really executes before the next row
		// fails, and abort retains that event without retaining its physical row.
		fixture.StatisticsExec(t,p,b,w,"SAVEPOINT streamed",0)
		streamed:=fixture.ImplicitError("INSERT INTO xat_atomic VALUES(2),(NULL) RETURNING n",fixture.Diagnostic{Code:"23502",Message:`null value in column "n" of relation "xat_atomic" violates not-null constraint`,Detail:"Failing row contains (null).",Schema:"public",Table:"xat_atomic",Column:"n"});streamed.ErrorStage="execute";fixture.RunStep(t,p.Ctx,w,b.Recorder,streamed)
		fixture.StatisticsExec(t,p,b,w,"ROLLBACK TO streamed",0)
		xactTableCounts(t,xactTableGet(t,p,w,"xat_atomic"),plainInserts+1,0,0)
		fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT count(*) FROM xat_atomic",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
		xactTableEnd(t,p,b,w,"ROLLBACK")
	})
}

func TestReferencePGStatXactAllTablesSnapshotIndependence(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for _,mode:=range []string{"none","cache","snapshot"}{
			name:="xat_"+mode;tableStatCreate(t,p,b,c,name)
			w,retire:=fixture.StatisticsReportingConn(t,p,b)
			fixture.StatisticsExec(t,p,b,w,"SET stats_fetch_consistency="+mode,0);fixture.StatisticsExec(t,p,b,w,"BEGIN",0)
			fixture.StatisticsExec(t,p,b,w,"INSERT INTO "+name+" VALUES(1,NULL)",1)
			retained:=xactTableGet(t,p,w,name);xactTableCounts(t,retained,1,0,0)
			cut:=tableStatGet(t,p,w,name);if cut.Inserts!=0{t.Fatal("unpublished insert reached cumulative cut",cut)}
			fixture.StatisticsExec(t,p,b,w,"INSERT INTO "+name+" VALUES(2,NULL)",1)
			xactTableCounts(t,xactTableGet(t,p,w,name),2,0,0)
			fixture.StatisticsClearSnapshot(t,p,b,w)
			xactTableCounts(t,xactTableGet(t,p,w,name),2,0,0)
			if got:=tableStatGet(t,p,w,name);got.Inserts!=0{t.Fatal("clear published local events",got)}
			fixture.StatisticsExec(t,p,b,w,"SET LOCAL track_counts=off",0)
			fixture.StatisticsExec(t,p,b,w,"INSERT INTO "+name+" VALUES(3,NULL)",1)
			xactTableCounts(t,xactTableGet(t,p,w,name),2,0,0)
			fixture.StatisticsExec(t,p,b,w,"SET LOCAL track_counts=on",0)
			fixture.StatisticsExec(t,p,b,w,"INSERT INTO "+name+" VALUES(4,NULL)",1)
			xactTableCounts(t,xactTableGet(t,p,w,name),3,0,0)
			xactTableCounts(t,retained,1,0,0)
			xactTableEnd(t,p,b,w,"ROLLBACK");retire()
		}
	})
}
