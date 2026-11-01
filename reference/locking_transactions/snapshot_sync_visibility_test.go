package reference_test

import (
	"database/sql/driver"
	"testing"

	"stuntgres/reference/fixture"
)

// Expected cuts follow the published MVCC history, not a native-generated
// golden. Every table (including an empty and an unread table) is witnessed.
func TestReferenceSnapshotSyncVisibility(t *testing.T) {
	snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, w, c := snapshotConn(t,p,b), snapshotConn(t,p,b), snapshotConn(t,p,b)
		syncSeed(t,p,w)
		fixture.LockExec(t,p.Ctx,w,"CREATE TABLE sync_u(id INT,v INT)",0)
		fixture.LockExec(t,p.Ctx,w,"CREATE TABLE sync_e(id INT,v INT)",0)
		fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_u VALUES(1,10)",1)
		fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
		token := syncExport(t,p,b,a,nil)
		fixture.LockExec(t,p.Ctx,w,"BEGIN",0)
		fixture.LockExec(t,p.Ctx,w,"UPDATE sync_t SET v=20 WHERE id=1",1)
		fixture.LockExec(t,p.Ctx,w,"DELETE FROM sync_t WHERE id=3",1)
		fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(2,200)",1)
		fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_u VALUES(2,20)",1)
		fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_e VALUES(3,30)",1)
		fixture.LockExec(t,p.Ctx,w,"COMMIT",0)
		fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0)
		syncImport(t,p,c,token)
		syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
		syncRows(t,p,b,c,"sync_u",[][]any{{int64(1),int64(10)}})
		syncRows(t,p,b,c,"sync_e",nil)
		syncRows(t,p,b,w,"sync_t",[][]any{{int64(1),int64(20)},{int64(2),int64(200)}})
		syncRows(t,p,b,w,"sync_u",[][]any{{int64(1),int64(10)},{int64(2),int64(20)}})
		syncRows(t,p,b,w,"sync_e",[][]any{{int64(3),int64(30)}})
		fixture.LockExec(t,p.Ctx,a,"COMMIT",0)
		syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
		syncExpired(t,p,w,token)
	})
}

func TestReferenceSnapshotSyncPrivateWrites(t *testing.T) {
	snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,w,c:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
		syncSeed(t,p,w)
		fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
		fixture.LockExec(t,p.Ctx,a,"INSERT INTO sync_t VALUES(9,90)",1)
		fixture.LockExec(t,p.Ctx,a,"UPDATE sync_t SET v=11 WHERE id=1",1)
		fixture.LockExec(t,p.Ctx,a,"DELETE FROM sync_t WHERE id=3",1)
		token:=syncExport(t,p,b,a,nil)
		fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0)
		syncImport(t,p,c,token)
		syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
		fixture.LockExec(t,p.Ctx,c,"INSERT INTO sync_t VALUES(8,80)",1)
		syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)},{int64(8),int64(80)}})
		syncRows(t,p,b,w,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
		fixture.LockExec(t,p.Ctx,a,"COMMIT",0)
		syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)},{int64(8),int64(80)}})
		fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0)
		syncRows(t,p,b,w,"sync_t",[][]any{{int64(1),int64(11)},{int64(9),int64(90)}})
	})
}

func TestReferenceSnapshotSyncMultipleExports(t *testing.T) {
	for _, isolation:=range []string{"READ COMMITTED","REPEATABLE READ"}{
		t.Run(isolation,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
			a,w,c:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
			fixture.LockExec(t,p.Ctx,w,"CREATE TABLE sync_t(id INT,v INT)",0)
			fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(1,10)",1)
			fixture.LockExec(t,p.Ctx,a,"BEGIN ISOLATION LEVEL "+isolation,0)
			x:=syncExport(t,p,b,a,nil)
			fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(2,20)",1)
			y:=syncExport(t,p,b,a,nil)
			if x==y {t.Fatal("separate exports reused identity")}
			for i,token:=range []string{x,y}{
				fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0)
				syncImport(t,p,c,token)
				want:=[][]any{{int64(1),int64(10)}}
				if i==1&&isolation=="READ COMMITTED" {want=append(want,[]any{int64(2),int64(20)})}
				syncRows(t,p,b,c,"sync_t",want)
				fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0)
			}
			var first,second string
			if err:=a.QueryRowContext(p.Ctx,"SELECT pg_export_snapshot(),pg_export_snapshot()").Scan(&first,&second);err!=nil{t.Fatal(err)}
			if first==second||first==x||second==y {t.Fatal("same-command exports reused identity")}
			for _,token:=range []string{first,second}{
				fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
				want:=[][]any{{int64(1),int64(10)}};if isolation=="READ COMMITTED"{want=append(want,[]any{int64(2),int64(20)})}
				syncRows(t,p,b,c,"sync_t",want);fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0)
			}
		})})
	}
}

func TestReferenceSnapshotSyncLifetime(t *testing.T) {
	for _,ending:=range []string{"COMMIT","ROLLBACK","physical-close","COMMIT AND CHAIN","ROLLBACK AND CHAIN"}{
		t.Run(ending,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
			a,w,c:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
			syncSeed(t,p,w);fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
			held,err:=a.PrepareContext(p.Ctx,"SELECT pg_export_snapshot()");if err!=nil{t.Fatal(err)}
			token:=syncExport(t,p,b,a,held);if err:=held.Close();err!=nil{t.Fatal(err)}
			fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
			fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(2,20)",1)
			if ending=="physical-close" {if err:=a.Raw(func(any)error{return driver.ErrBadConn});err!=driver.ErrBadConn{t.Fatalf("physical close: %v",err)}} else {fixture.LockExec(t,p.Ctx,a,ending,0)}
			syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
			syncExpired(t,p,w,token)
			if ending!="physical-close" {fixture.LockExec(t,p.Ctx,a,"ROLLBACK",0);fixture.LockExec(t,p.Ctx,a,"BEGIN",0);next:=syncExport(t,p,b,a,nil);if next==token{t.Fatal("new incarnation reused token")}}
		})})
	}
	t.Run("implicit",func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){a,c:=snapshotConn(t,p,b),snapshotConn(t,p,b);token:=syncExport(t,p,b,a,nil);syncExpired(t,p,c,token)})})
}

func TestReferenceSnapshotSyncFeatureSmoke(t *testing.T) {
	snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,w,c:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
		fixture.LockExec(t,p.Ctx,w,"CREATE TABLE sync_t(id INT PRIMARY KEY,v INT)",0);fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(1,10)",1)
		fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
		held,err:=a.PrepareContext(p.Ctx,"SELECT pg_export_snapshot()");if err!=nil{t.Fatal(err)}
		token:=syncExport(t,p,b,a,held);if err:=held.Close();err!=nil{t.Fatal(err)}
		fixture.LockExec(t,p.Ctx,w,"UPDATE sync_t SET v=20 WHERE id=1",1);fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(2,200)",1)
		fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
		syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)}})
		fixture.LockExec(t,p.Ctx,w,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,w,token);fixture.LockExec(t,p.Ctx,w,"ROLLBACK",0)
		fixture.LockExec(t,p.Ctx,a,"COMMIT",0);syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)}});syncExpired(t,p,w,token);fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0)
		fixture.LockExec(t,p.Ctx,a,"BEGIN",0);fixture.LockExec(t,p.Ctx,a,"INSERT INTO sync_t VALUES(9,90)",1);token=syncExport(t,p,b,a,nil)
		fixture.PreparedReferenceCleanup(t,b,"sync_smoke")
		syncError(t,p,a,"PREPARE TRANSACTION 'sync_smoke'","0A000","cannot PREPARE a transaction that has exported snapshots")
		fixture.PreparedReferenceAbsent(t,p,w,"sync_smoke");syncExpired(t,p,w,token)
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT 1 AS healthy",[]string{"healthy"},[]string{"INT4"},[][]any{{int64(1)}}))
		syncRows(t,p,b,w,"sync_t",[][]any{{int64(1),int64(20)},{int64(2),int64(200)}})
	})
}
