package reference_test

import (
	"database/sql"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncSerializable(t *testing.T) {
	for _,mode:=range []string{"REPEATABLE READ","SERIALIZABLE","SERIALIZABLE READ ONLY"}{
		t.Run(mode,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
			a,w,c:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
			syncSeed(t,p,w);fixture.LockExec(t,p.Ctx,a,"BEGIN ISOLATION LEVEL SERIALIZABLE",0);token:=syncExport(t,p,b,a,nil)
			fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(2,20)",1)
			fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL "+mode,0);syncImport(t,p,c,token)
			syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
			fixture.LockExec(t,p.Ctx,a,"COMMIT",0);fixture.LockExec(t,p.Ctx,c,"COMMIT",0)
		})})
	}
	for _,held:=range []bool{false,true}{
		t.Run(map[bool]string{false:"skew-direct",true:"skew-held"}[held],func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
			source,c,d:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
			fixture.LockExec(t,p.Ctx,c,"CREATE TABLE sync_left(v INT)",0);fixture.LockExec(t,p.Ctx,c,"CREATE TABLE sync_right(v INT)",0)
			fixture.LockExec(t,p.Ctx,c,"INSERT INTO sync_left VALUES(10)",1);fixture.LockExec(t,p.Ctx,c,"INSERT INTO sync_right VALUES(20)",1)
			fixture.LockExec(t,p.Ctx,source,"BEGIN ISOLATION LEVEL SERIALIZABLE",0);token:=syncExport(t,p,b,source,nil)
			// This committed row advances the data frontier after export. Both
			// imported transactions must nevertheless read the old exact cuts.
			fixture.LockExec(t,p.Ctx,c,"INSERT INTO sync_left VALUES(99)",1)
			for _,x:=range []*sql.Conn{c,d}{fixture.LockExec(t,p.Ctx,x,"BEGIN ISOLATION LEVEL SERIALIZABLE",0);syncImport(t,p,x,token)}
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.TransactionRows(false,"SELECT v FROM sync_left ORDER BY v",10))
			fixture.RunStep(t,p.Ctx,d,b.Recorder,fixture.TransactionRows(false,"SELECT v FROM sync_right ORDER BY v",20))
			ec:=serializableReferenceWrite(t,p.Ctx,c,"INSERT INTO sync_right VALUES(30)",held,1)
			ed:=serializableReferenceWrite(t,p.Ctx,d,"INSERT INTO sync_left VALUES(40)",held,1)
			commit:=func(x *sql.Conn,prior error)bool{if prior!=nil{return false};_,err:=x.ExecContext(p.Ctx,"COMMIT");if err!=nil{serializableReferenceFailure(t,err);return false};return true}
			okC,okD:=commit(c,ec),commit(d,ed)
			if okC==okD{t.Fatal("expected exactly one real SSI participant to commit",okC,okD)}
			serializableReferenceRollback(p.Ctx,c);serializableReferenceRollback(p.Ctx,d)
			fixture.LockExec(t,p.Ctx,source,"ROLLBACK",0)
		})})
	}
}
