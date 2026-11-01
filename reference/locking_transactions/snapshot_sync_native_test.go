package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncNativeBoundaries(t *testing.T) {
	for _,replacement:=range []string{"Reset","Restore"}{t.Run(replacement,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,w,c:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
		syncSeed(t,p,w);fixture.LockExec(t,p.Ctx,a,"BEGIN",0);fixture.LockExec(t,p.Ctx,a,"INSERT INTO sync_t VALUES(9,90)",1);token:=syncExport(t,p,b,a,nil)
		fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
		rows,err:=c.QueryContext(p.Ctx,"SELECT v FROM sync_t ORDER BY v");if err!=nil{t.Fatal(err)};defer rows.Close()
		if !b.PG {
			var dump bytes.Buffer
			if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)}
			if strings.Contains(dump.String(),token){t.Fatal("Dump serialized live snapshot transport")}
			connector,err:=stuntgres.NewDriver().OpenConnector(t.Name()+"/clone");if err!=nil{t.Fatal(err)}
			clone:=sql.OpenDB(connector);defer clone.Close()
			if err:=stuntgres.Restore(p.Ctx,clone,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
			cloneConn,err:=clone.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer cloneConn.Close()
			syncExpired(t,p,cloneConn,token)
			cloneRows,err:=cloneConn.QueryContext(p.Ctx,"SELECT id,v FROM sync_t ORDER BY id");if err!=nil{t.Fatal(err)}
			names,err:=cloneRows.Columns();if err!=nil||!reflect.DeepEqual(names,[]string{"id","v"}){cloneRows.Close();t.Fatal(names,err)}
			types,err:=cloneRows.ColumnTypes();if err!=nil||len(types)!=2||types[0].DatabaseTypeName()!="INT4"||types[1].DatabaseTypeName()!="INT4"{cloneRows.Close();t.Fatal(types,err)}
			var restored [][]any
			for cloneRows.Next(){var id,value any;if err:=cloneRows.Scan(&id,&value);err!=nil{cloneRows.Close();t.Fatal(err)};restored=append(restored,[]any{id,value})}
			if err:=cloneRows.Err();err!=nil{cloneRows.Close();t.Fatal(err)}
			if err:=cloneRows.Close();err!=nil{t.Fatal(err)}
			if _,equal:=fixture.CompareRows(restored,[][]any{{int64(1),int64(10)},{int64(3),int64(30)}},false);!equal{t.Fatal("restored committed rows",restored)}
			if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader("CREATE TABLE broken(x INT);INSERT INTO broken VALUES('bad');"));err==nil{t.Fatal("invalid Restore succeeded")}
			canceled,cancel:=context.WithCancel(p.Ctx);cancel()
			if err:=stuntgres.Restore(canceled,p.Native,bytes.NewReader(dump.Bytes()));!errors.Is(err,context.Canceled){t.Fatalf("canceled Restore: %v",err)}
			// Neither failed publication changed source lifetime or its cut.
			fixture.LockExec(t,p.Ctx,w,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,w,token);fixture.LockExec(t,p.Ctx,w,"ROLLBACK",0)
			if replacement=="Reset"{err=stuntgres.Reset(p.Ctx,p.Native)}else{err=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()))};if err!=nil{t.Fatal(err)}
			_,err=c.ExecContext(p.Ctx,"SELECT 1")
			if err==nil||err.Error()!="stuntgres: catalog replaced before publication"{t.Fatalf("active importer fence: %v",err)}
			if _,ok:=err.(interface{SQLState()string});ok{t.Fatal("replacement fabricated SQLSTATE")}
			syncExpired(t,p,w,token)
		}
		fixture.LimitLockValues(t,fixture.RelationOutcome{Rows:rows},b.PG,10,30)
	})})}
	t.Run("whole-abort",func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,c:=snapshotConn(t,p,b),snapshotConn(t,p,b);pid:=fixture.LockPID(t,p.Ctx,a);fixture.LockExec(t,p.Ctx,a,"BEGIN",0);token:=syncExport(t,p,b,a,nil)
		syncError(t,p,a,"SELECT 1/0","22012","division by zero");syncAborted(t,p,c,token,pid)
		fixture.LockExec(t,p.Ctx,a,"ROLLBACK",0);syncExpired(t,p,c,token)
	})})
}

func TestReferenceSnapshotSyncReexport(t *testing.T) {
	snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,w,c:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b);syncSeed(t,p,w)
		fixture.LockExec(t,p.Ctx,a,"BEGIN",0);token:=syncExport(t,p,b,a,nil)
		fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
		fixture.LockExec(t,p.Ctx,a,"COMMIT",0);fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(2,20)",1)
		next:=syncExport(t,p,b,c,nil);if next==token{t.Fatal("reexport reused source lifetime identity")}
		fixture.LockExec(t,p.Ctx,w,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,w,next)
		syncRows(t,p,b,w,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
		fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0);syncRows(t,p,b,w,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
		fixture.LockExec(t,p.Ctx,w,"ROLLBACK",0);syncExpired(t,p,w,next)
	})
}
