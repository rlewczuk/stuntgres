package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncPreparedImport(t *testing.T) {
	for _,lane:=range []string{"live","expired","invalid","read-committed"}{t.Run(lane,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,w,c:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
		syncSeed(t,p,w);fixture.LockExec(t,p.Ctx,a,"BEGIN",0);token:=syncExport(t,p,b,a,nil)
		if lane=="invalid"{token="bad"}
		begin:="BEGIN ISOLATION LEVEL REPEATABLE READ";if lane=="read-committed"{begin="BEGIN"}
		fixture.LockExec(t,p.Ctx,c,begin,0)
		query:="SET TRANSACTION SNAPSHOT "+snapshotQuote(token)
		held,err:=c.PrepareContext(p.Ctx,query)
		if err!=nil{t.Fatal("Describe imported or validated runtime state",err)};defer held.Close()
		fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(2,20)",1)
		if lane=="expired"{fixture.LockExec(t,p.Ctx,a,"COMMIT",0)}
		result,err:=held.ExecContext(p.Ctx)
		if lane=="live"{
			if err!=nil{t.Fatal("held import acquired a snapshot during Describe",err)};fixture.LockAffected(t,result,0)
			if err:=held.Close();err!=nil{t.Fatal(err)}
			syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
			fixture.LockExec(t,p.Ctx,a,"COMMIT",0)
			syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
		}else{
			if result!=nil{t.Fatal("failed held import returned a result")}
			want:=fixture.ControlsReferenceDiagnostic("42704","snapshot \""+token+"\" does not exist")
			if lane=="invalid"{want=fixture.ControlsReferenceDiagnostic("22023","invalid snapshot identifier: \"bad\"")}
			if lane=="read-committed"{want=fixture.ControlsReferenceDiagnostic("0A000","a snapshot-importing transaction must have isolation level SERIALIZABLE or REPEATABLE READ")}
			fixture.AssertDiagnostic(t,err,&want)
		}
	})})}
}
