package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncTypedCalls(t *testing.T) {
	snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,c:=snapshotConn(t,p,b),snapshotConn(t,p,b)
		fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
		held,err:=a.PrepareContext(p.Ctx,"SELECT pg_export_snapshot()");if err!=nil{t.Fatal(err)};defer held.Close()
		// Parse/Describe must not set the sticky export prohibition.
		fixture.PreparedReferenceCleanup(t,b,"sync_describe")
		fixture.LockExec(t,p.Ctx,a,"PREPARE TRANSACTION 'sync_describe'",0)
		fixture.LockExec(t,p.Ctx,c,"ROLLBACK PREPARED 'sync_describe'",0)
		fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
		x:=syncExport(t,p,b,a,nil);y:=syncExport(t,p,b,a,held);z:=syncExport(t,p,b,a,held)
		if x==y||x==z||y==z{t.Fatal("held execution cached token")}
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT pg_typeof(pg_catalog.pg_export_snapshot())::oid::bigint AS oid",[]string{"oid"},[]string{"INT8"},[][]any{{int64(25)}}))
		for _,token:=range []string{x,y,z}{fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token);fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0)}
	})
	const hint="No function matches the given name and argument types. You might need to add explicit type casts."
	for _,tc:=range []struct{call,signature string}{
		{"pg_export_snapshot(1)","pg_export_snapshot(integer)"},
		{"pg_export_snapshot(TRUE)","pg_export_snapshot(boolean)"},
		{"pg_export_snapshot(NULL)","pg_export_snapshot(unknown)"},
		{"pg_export_snapshot('x'::text)","pg_export_snapshot(text)"},
		{"pg_export_snapshot(1,2)","pg_export_snapshot(integer, integer)"},
		{"pg_export_snapshot('x'::text,'y'::text)","pg_export_snapshot(text, text)"},
		{"public.pg_export_snapshot()","public.pg_export_snapshot()"},
		{"\"PG_EXPORT_SNAPSHOT\"()","PG_EXPORT_SNAPSHOT()"},
	}{t.Run(tc.call,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){identityError(t,p,b,snapshotConn(t,p,b),"SELECT "+tc.call,fixture.Diagnostic{Code:"42883",Message:"function "+tc.signature+" does not exist",Hint:hint,Position:"8"},"prepare")})})}
	for _,tc:=range []struct{query,token,pos string}{
		{"SET TRANSACTION SNAPSHOT NULL","NULL","26"},
		{"SET TRANSACTION SNAPSHOT $1","$1","26"},
		{"SET TRANSACTION SNAPSHOT 'ABC' junk","junk","32"},
		{"/*é*/ SET TRANSACTION SNAPSHOT NULL","NULL","32"},
	}{t.Run(tc.query,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){identityError(t,p,b,snapshotConn(t,p,b),tc.query,fixture.Diagnostic{Code:"42601",Message:"syntax error at or near \""+tc.token+"\"",Position:tc.pos},"prepare")})})}
}

func TestReferenceSnapshotSyncImportRules(t *testing.T) {
	const late="SET TRANSACTION SNAPSHOT must be called before any query"
	const isolation="a snapshot-importing transaction must have isolation level SERIALIZABLE or REPEATABLE READ"
	for _,tc:=range []struct{name,begin,prior,token,code,message string}{
		{"rc","BEGIN","","bad","0A000",isolation},
		{"ru","BEGIN ISOLATION LEVEL READ UNCOMMITTED","","bad","0A000",isolation},
		{"rr-invalid","BEGIN ISOLATION LEVEL REPEATABLE READ","","bad","22023","invalid snapshot identifier: \"bad\""},
		{"rr-missing","BEGIN ISOLATION LEVEL REPEATABLE READ","","ABC","42704","snapshot \"ABC\" does not exist"},
		{"rr-legal-short","BEGIN ISOLATION LEVEL REPEATABLE READ","","-","42704","snapshot \"-\" does not exist"},
		{"rr-empty","BEGIN ISOLATION LEVEL REPEATABLE READ","","","XX000",`could not read file "pg_snapshots/": Is a directory`},
		{"late-invalid","BEGIN ISOLATION LEVEL REPEATABLE READ","SELECT 1","bad","25001",late},
		{"late-valid","BEGIN ISOLATION LEVEL REPEATABLE READ","SELECT 1","live","25001",late},
		{"xid","BEGIN ISOLATION LEVEL REPEATABLE READ","SELECT pg_current_xact_id()","live","25001",late},
		{"child","BEGIN ISOLATION LEVEL REPEATABLE READ","SAVEPOINT child","live","25001",late},
		{"repeat","BEGIN ISOLATION LEVEL REPEATABLE READ","import","live","25001",late},
		{"prepare-query","BEGIN ISOLATION LEVEL REPEATABLE READ","prepare","live","25001",late},
	}{t.Run(tc.name,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,c:=snapshotConn(t,p,b),snapshotConn(t,p,b);fixture.LockExec(t,p.Ctx,a,"BEGIN",0);live:=syncExport(t,p,b,a,nil)
		fixture.LockExec(t,p.Ctx,c,tc.begin,0)
		switch tc.prior{case "":case "import":syncImport(t,p,c,live);case "prepare":h,err:=c.PrepareContext(p.Ctx,"SELECT 1");if err!=nil{t.Fatal(err)};h.Close();default:rows,err:=c.QueryContext(p.Ctx,tc.prior);if err!=nil{t.Fatal(err)};for rows.Next(){};if err:=rows.Close();err!=nil{t.Fatal(err)}}
		token:=tc.token;if token=="live"{token=live};syncError(t,p,c,"SET TRANSACTION SNAPSHOT "+snapshotQuote(token),tc.code,tc.message)
		syncError(t,p,c,"SELECT 1","25P02","current transaction is aborted, commands ignored until end of transaction block")
	})})}
	for _,tc:=range []struct{name,source,importer,message string}{
		{"serializable-source-required","BEGIN ISOLATION LEVEL REPEATABLE READ","BEGIN ISOLATION LEVEL SERIALIZABLE","a serializable transaction cannot import a snapshot from a non-serializable transaction"},
		{"serializable-writer-source","BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY","BEGIN ISOLATION LEVEL SERIALIZABLE","a non-read-only serializable transaction cannot import a snapshot from a read-only transaction"},
		{"deferrable","BEGIN ISOLATION LEVEL SERIALIZABLE","BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE","a snapshot-importing transaction must not be READ ONLY DEFERRABLE"},
	}{t.Run(tc.name,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){a,c:=snapshotConn(t,p,b),snapshotConn(t,p,b);fixture.LockExec(t,p.Ctx,a,tc.source,0);token:=syncExport(t,p,b,a,nil);fixture.LockExec(t,p.Ctx,c,tc.importer,0);syncError(t,p,c,"SET TRANSACTION SNAPSHOT "+snapshotQuote(token),"0A000",tc.message)})})}
}

func TestReferenceSnapshotSyncSavepoints(t *testing.T) {
	snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,w,c:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b);syncSeed(t,p,w)
		fixture.LockExec(t,p.Ctx,a,"BEGIN",0);token:=syncExport(t,p,b,a,nil)
		fixture.LockExec(t,p.Ctx,a,"SAVEPOINT child",0)
		syncError(t,p,a,"SELECT pg_export_snapshot()","25001","cannot export a snapshot from a subtransaction")
		fixture.LockExec(t,p.Ctx,a,"ROLLBACK TO child",0);fixture.LockExec(t,p.Ctx,a,"RELEASE child",0)
		next:=syncExport(t,p,b,a,nil);if next==token{t.Fatal("released child reused export")}
		fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(2,20)",1)
		fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
		fixture.LockExec(t,p.Ctx,c,"SAVEPOINT own",0);fixture.LockExec(t,p.Ctx,c,"INSERT INTO sync_t VALUES(8,80)",1);fixture.LockExec(t,p.Ctx,c,"ROLLBACK TO own",0);fixture.LockExec(t,p.Ctx,c,"RELEASE own",0)
		syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
		syncError(t,p,c,"SET TRANSACTION SNAPSHOT "+snapshotQuote(next),"25001","SET TRANSACTION SNAPSHOT must be called before any query")
		fixture.PreparedReferenceCleanup(t,b,"sync_sticky")
		syncError(t,p,a,"PREPARE TRANSACTION 'sync_sticky'","0A000","cannot PREPARE a transaction that has exported snapshots")
		syncExpired(t,p,w,token);syncExpired(t,p,w,next)
	})
}

func TestReferenceSnapshotSyncPrepareReset(t *testing.T) {
	snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,w:=snapshotConn(t,p,b),snapshotConn(t,p,b);syncSeed(t,p,w)
		fixture.LockExec(t,p.Ctx,a,"SET application_name='snapshot_baseline'",0)
		fixture.LockExec(t,p.Ctx,a,"BEGIN",0);fixture.LockExec(t,p.Ctx,a,"SET application_name='snapshot_private'",0);fixture.LockExec(t,p.Ctx,a,"INSERT INTO sync_t VALUES(9,90)",1)
		token:=syncExport(t,p,b,a,nil);fixture.PreparedReferenceCleanup(t,b,"sync_reset")
		syncError(t,p,a,"PREPARE TRANSACTION 'sync_reset'","0A000","cannot PREPARE a transaction that has exported snapshots")
		fixture.PreparedReferenceAbsent(t,p,w,"sync_reset");syncExpired(t,p,w,token)
		syncRows(t,p,b,w,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT current_setting('application_name') AS name,1 AS healthy",[]string{"name","healthy"},[]string{"TEXT","INT4"},[][]any{{"snapshot_baseline",int64(1)}}))
		fixture.LockExec(t,p.Ctx,a,"BEGIN",0);fixture.LockExec(t,p.Ctx,a,"PREPARE TRANSACTION 'sync_reset'",0);fixture.PreparedReferenceOne(t,p,w,"sync_reset");fixture.LockExec(t,p.Ctx,w,"ROLLBACK PREPARED 'sync_reset'",0)
	})
}

func TestReferenceSnapshotSyncOutsideImport(t *testing.T) {
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
		f:=fixture.NewControlsReferenceFixture(t,p,b.PG)
		const query="SET TRANSACTION SNAPSHOT 'bad'"
		held,err:=f.A.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};defer held.Close()
		f.NoticesWant(t,"","")
		for _,prepared:=range []bool{false,true}{
			if prepared{_,err=held.ExecContext(p.Ctx)}else{_,err=f.A.ExecContext(p.Ctx,query)}
			want:=fixture.ControlsReferenceDiagnostic("0A000","a snapshot-importing transaction must have isolation level SERIALIZABLE or REPEATABLE READ")
			fixture.AssertDiagnostic(t,err,&want)
			f.NoticesWant(t,"25P01","SET TRANSACTION can only be used in transaction blocks")
			fixture.RunStep(t,p.Ctx,f.A,f.Recorder,fixture.Select("SELECT 1 AS healthy",[]string{"healthy"},[]string{"INT4"},[][]any{{int64(1)}}))
		}
	})}
}
