package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAutoincTransactions(t *testing.T) {
	for _,later:=range []string{"attribute","sequence","not-null","check","multirow"} {
		t.Run("atomic/"+later,func(t *testing.T) {
			triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				userOperatorExec(t,p,b,c,"CREATE EXTENSION autoinc","CREATE SEQUENCE s START 10","CREATE TABLE t(id integer,k integer,label text)")
				args:="'id','s'"
				q,code,message:="INSERT INTO t VALUES(NULL,NULL,'bad')","09000",`"t" has no attribute "missing"`
				switch later {
				case "attribute":args+=",'missing','s'"
				case "sequence":args+=",'k','absent'";code="42P01";message=`relation "absent" does not exist`
				case "not-null":userOperatorExec(t,p,b,c,"ALTER TABLE t ALTER COLUMN k SET NOT NULL");code="23502";message=`null value in column "k" of relation "t" violates not-null constraint`
				case "check":userOperatorExec(t,p,b,c,"ALTER TABLE t ADD CONSTRAINT positive_k CHECK(k>0)");q="INSERT INTO t VALUES(NULL,-1,'bad')";code="23514";message=`new row for relation "t" violates check constraint "positive_k"`
				case "multirow":args+=",'k','absent'";q="INSERT INTO t VALUES(NULL,7,'first'),(NULL,NULL,'bad')";code="42P01";message=`relation "absent" does not exist`
				}
				userOperatorExec(t,p,b,c,"CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc("+args+")")
				run:=autoincRoute(t,p,b,c,"direct","")
				s:=autoincError(q,code,message)
				if later=="not-null" { s.Diagnostic.Schema="public";s.Diagnostic.Table="t";s.Diagnostic.Column="k";s.Diagnostic.Detail="Failing row contains (10, null, bad)." }
				if later=="check" { s.Diagnostic.Schema="public";s.Diagnostic.Table="t";s.Diagnostic.Constraint="positive_k";s.Diagnostic.Detail="Failing row contains (10, -1, bad)." }
				run(s)
				n:=int64(10);if later=="multirow" { n=11 }
				run(autoincScalar("SELECT currval('s')","currval",n));run(autoincScalar("SELECT count(*) FROM t","count",0))
				peer,err:=b.DB.Conn(p.Ctx);if err!=nil { t.Fatal(err) };defer peer.Close()
				fixture.RunStep(t,p.Ctx,peer,b.Recorder,autoincScalar("SELECT nextval('s')","nextval",n+1))
			})
		})
	}
	t.Run("rollback-savepoint-restart",func(t *testing.T) {
		triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			userOperatorExec(t,p,b,c,"CREATE EXTENSION autoinc","CREATE SEQUENCE s START 10","CREATE TABLE t(id integer,label text)","CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc('id','s')")
			run:=autoincRoute(t,p,b,c,"held","INSERT INTO t VALUES($1,$2)")
			peer,err:=b.DB.Conn(p.Ctx);if err!=nil { t.Fatal(err) };defer peer.Close()
			peerStep:=func(s fixture.Step) { fixture.RunStep(t,p.Ctx,peer,b.Recorder,s) }
			run(fixture.Exec("INSERT INTO t VALUES($1,$2)",1,nil,"committed"))
			peerStep(autoincScalar("SELECT nextval('s')","nextval",11))
			run(fixture.Exec("BEGIN",0));defer c.ExecContext(context.Background(),"ROLLBACK")
			run(fixture.Exec("INSERT INTO t VALUES($1,$2)",1,nil,"rolled"));run(fixture.Exec("ROLLBACK",0))
			run(autoincScalar("SELECT currval('s')","currval",12));peerStep(autoincScalar("SELECT nextval('s')","nextval",13))
			run(fixture.Exec("BEGIN",0));run(fixture.Exec("SAVEPOINT a",0));run(fixture.Exec("INSERT INTO t VALUES($1,$2)",1,nil,"savepoint"));run(fixture.Exec("ROLLBACK TO a",0))
			run(autoincScalar("SELECT currval('s')","currval",14));run(fixture.Exec("COMMIT",0));peerStep(autoincScalar("SELECT nextval('s')","nextval",15))
			run(fixture.Exec("BEGIN",0));run(fixture.Exec("ALTER SEQUENCE s RESTART WITH 90",0));run(fixture.Exec("INSERT INTO t VALUES($1,$2)",1,nil,"restart"));run(fixture.Exec("ROLLBACK",0))
			peerStep(autoincScalar("SELECT nextval('s')","nextval",16))
			run(autoincRows("SELECT id,label FROM t ORDER BY label",[]any{int64(10),"committed"}))
		})
	})
	t.Run("restart-demand-wait-cancel",autoincDemandCancellation)
	t.Run("restart-demand-timeout",func(t *testing.T) {
		triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			autoincSeed(t,p,b,c)
			holder,err:=b.DB.Conn(p.Ctx);if err!=nil { t.Fatal(err) };defer holder.Close();defer holder.ExecContext(context.Background(),"ROLLBACK")
			run:=autoincRoute(t,p,b,c,"held","INSERT INTO t VALUES(0,'wait')")
			fixture.LockExec(t,p.Ctx,holder,"BEGIN",0);fixture.LockExec(t,p.Ctx,holder,"ALTER SEQUENCE s RESTART WITH 90",0)
			run(fixture.Exec("SET lock_timeout='10ms'",0))
			run(autoincError("INSERT INTO t VALUES(0,'wait')","55P03","canceling statement due to lock timeout"))
			fixture.LockExec(t,p.Ctx,holder,"ROLLBACK",0)
			fixture.RunStep(t,p.Ctx,holder,b.Recorder,autoincRows("INSERT INTO t VALUES(NULL,'healthy') RETURNING id,label",[]any{int64(10),"healthy"}))
		})
	})
}

func autoincDemandCancellation(t *testing.T) {
	triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		autoincSeed(t,p,b,c)
		holder,err:=b.DB.Conn(p.Ctx);if err!=nil { t.Fatal(err) };defer holder.Close();defer holder.ExecContext(context.Background(),"ROLLBACK")
		q:="INSERT INTO t VALUES(0,'wait')"
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil { t.Fatal(err) };defer stmt.Close()
		waiterPID,holderPID:=0,0;var oid int64
		if b.PG { waiterPID=fixture.LockPID(t,p.Ctx,c);holderPID=fixture.LockPID(t,p.Ctx,holder);oid=fixture.LockOID(t,p.Ctx,"s") }
		fixture.LockExec(t,p.Ctx,holder,"BEGIN",0);fixture.LockExec(t,p.Ctx,holder,"ALTER SEQUENCE s RESTART WITH 90",0)
		// Nonzero values do not acquire an allocation lock on s.
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO t VALUES(7,'nonzero')",1))
		fixture.LockExec(t,p.Ctx,c,"BEGIN",0);defer c.ExecContext(context.Background(),"ROLLBACK")
		fixture.LockExec(t,p.Ctx,c,"SAVEPOINT demand",0)
		fixture.BinaryConversionActiveCancelAfter(t,p,b,c,q,"(*relationRequest).wait",func(ctx context.Context) {
			if b.PG { fixture.LockWaitBehind(t,ctx,waiterPID,holderPID,oid,"RowExclusiveLock") }
		},func(ctx context.Context) fixture.RelationOutcome {
			r,e:=stmt.ExecContext(ctx);return fixture.RelationOutcome{Result:r,Err:e}
		})
		fixture.LockExec(t,p.Ctx,c,"ROLLBACK TO demand",0);fixture.LockExec(t,p.Ctx,holder,"ROLLBACK",0)
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},b.Recorder,fixture.Exec(q,1))
		fixture.LockExec(t,p.Ctx,c,"COMMIT",0)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,autoincRows("SELECT id,label FROM t ORDER BY label",[]any{int64(7),"nonzero"},[]any{int64(10),"wait"}))
	})
}
