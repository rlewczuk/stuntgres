package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatUserFunctionsPublicationAndSnapshots(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xactFunctionCreate(t,p,b,c,"uf_pending","RETURN 7;")
		userFunctionAbsent(t,p,b,c,"uf_pending")
		unpublishedHeld:=fixture.IdentityHeld(t,p.Ctx,c,userFunctionSQL);defer unpublishedHeld.Statement.Close()
		w,retire:=fixture.StatisticsReportingConn(t,p,b);xactFunctionBegin(t,p,b,w)
		for range 2{xactFunctionCall(t,p,b,w,"uf_pending",7)}
		xactFunctionCounts(t,b,xactFunctionGet(t,p,w,"uf_pending"),2,2,2)
		fixture.StatisticsClearSnapshot(t,p,b,c)
		for _,q:=range []fixture.Executor{c,unpublishedHeld}{userFunctionUnpublished(t,p,b,q,"uf_pending");fixture.StatisticsClearSnapshot(t,p,b,c)}
		stmt,err:=c.PrepareContext(p.Ctx,userFunctionSQL);if err!=nil{t.Fatal(err)};stmt.Close();userFunctionUnpublished(t,p,b,c,"uf_pending");xactFunctionCounts(t,b,xactFunctionGet(t,p,w,"uf_pending"),2,2,2)
		fixture.StatisticsExec(t,p,b,w,"COMMIT",0);retire();userFunctionAwait(t,p,b,"uf_pending",2);fixture.StatisticsClearSnapshot(t,p,b,c);xactFunctionCounts(t,b,userFunctionGet(t,p,c,"uf_pending"),2,2,2)
		for _,mode:=range []string{"none","cache","snapshot"}{
			positive,negative,uncut:="uf_"+mode+"_positive","uf_"+mode+"_negative","uf_"+mode+"_uncut"
			for _,name:=range []string{positive,negative,uncut}{xactFunctionCreate(t,p,b,c,name,"RETURN 7;")}
			userFunctionPublish(t,p,b,positive,1,1)
			fixture.StatisticsExec(t,p,b,c,"SET stats_fetch_consistency="+mode,0);fixture.StatisticsExec(t,p,b,c,"BEGIN",0)
			held:=fixture.IdentityHeld(t,p.Ctx,c,userFunctionSQL)
			old:=userFunctionGet(t,p,held,positive);userFunctionAbsent(t,p,b,c,negative)
			pending,retirePending:=fixture.StatisticsReportingConn(t,p,b);xactFunctionBegin(t,p,b,pending)
			for range 2{xactFunctionCall(t,p,b,pending,negative,7)};xactFunctionCall(t,p,b,pending,uncut,7)
			xactFunctionCounts(t,b,xactFunctionGet(t,p,pending,negative),2,2,2)
			probe,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};fixture.StatisticsExec(t,p,b,probe,"SET stats_fetch_consistency=none",0)
			userFunctionUnpublished(t,p,b,probe,negative);userFunctionUnpublished(t,p,b,probe,uncut);probe.Close()
			if mode=="none"{userFunctionUnpublished(t,p,b,c,negative)}else{userFunctionAbsent(t,p,b,c,negative)}
			if mode=="snapshot"{userFunctionAbsent(t,p,b,c,uncut)}else{userFunctionUnpublished(t,p,b,c,uncut)}
			fixture.StatisticsClearSnapshot(t,p,b,c)
			zero:=userFunctionGet(t,p,held,negative);xactFunctionCounts(t,b,zero,0,0,0);if zero.Total!=0||zero.Self!=0{t.Fatal("shared admission leaked timing",zero)}
			userFunctionUnpublished(t,p,b,c,uncut);xactFunctionCounts(t,b,userFunctionGet(t,p,held,positive),1,1,1)
			fixture.StatisticsExec(t,p,b,pending,"COMMIT",0);retirePending();userFunctionAwait(t,p,b,negative,2);userFunctionAwait(t,p,b,uncut,1)
			userFunctionPublish(t,p,b,positive,1,2)
			want:=int64(1);if mode=="none"{want=2};xactFunctionCounts(t,b,userFunctionGet(t,p,held,positive),want,float64(want),float64(want))
			if mode=="none"{xactFunctionCounts(t,b,userFunctionGet(t,p,held,negative),2,2,2);xactFunctionCounts(t,b,userFunctionGet(t,p,c,uncut),1,1,1)}else{userFunctionUnpublished(t,p,b,held,negative);userFunctionUnpublished(t,p,b,c,uncut)}
			fixture.StatisticsClearSnapshot(t,p,b,c);xactFunctionCounts(t,b,userFunctionGet(t,p,held,positive),2,2,2);xactFunctionCounts(t,b,userFunctionGet(t,p,c,negative),2,2,2);xactFunctionCounts(t,b,userFunctionGet(t,p,c,uncut),1,1,1)
			if zero.Calls!=0||zero.Total!=0||zero.Self!=0{t.Fatal("retained zero cut mutated",zero)}
			if old.Calls!=1||old.Name!=positive{t.Fatal("retained cut mutated",old)}
			held.Statement.Close();fixture.StatisticsExec(t,p,b,c,"ROLLBACK",0)
		}
	})
}

func TestReferencePGStatUserFunctionsXactBoundary(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for _,end:=range []string{"COMMIT","ROLLBACK"}{
			name:="uf_boundary_commit";if end=="ROLLBACK"{name="uf_boundary_abort"};xactFunctionCreate(t,p,b,c,name,"RETURN 7;")
			w,retire:=userFunctionFreshReporter(t,p,b);xactFunctionBegin(t,p,b,w)
			held:=fixture.IdentityHeld(t,p.Ctx,w,xactFunctionSQL);xactFunctionCall(t,p,b,w,name,7);xactFunctionCounts(t,b,xactFunctionGet(t,p,held,name),1,1,1)
			fixture.StatisticsExec(t,p,b,w,"SAVEPOINT child",0);xactFunctionCall(t,p,b,w,name,7);fixture.StatisticsExec(t,p,b,w,"ROLLBACK TO child",0);fixture.StatisticsExec(t,p,b,w,"RELEASE child",0)
			xactFunctionCounts(t,b,xactFunctionGet(t,p,held,name),2,2,2);userFunctionUnpublished(t,p,b,c,name)
			// The fresh native backend owns its first unthrottled report;
			// PostgreSQL's existing helper requests the next terminal report.
			xactTableEnd(t,p,b,w,end);fixture.StatisticsExec(t,p,b,w,"BEGIN",0);xactFunctionAbsent(t,p,b,w,name);fixture.StatisticsExec(t,p,b,w,"ROLLBACK",0);held.Statement.Close();retire()
			userFunctionAwait(t,p,b,name,2);fixture.StatisticsClearSnapshot(t,p,b,c);xactFunctionCounts(t,b,userFunctionGet(t,p,c,name),2,2,2)
		}
	})
}
