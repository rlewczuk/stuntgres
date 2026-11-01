package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncAlterAtomicity(t *testing.T) {
	for _,route:=range []string{"check","default","index"} {
		for _,held:=range []bool{false,true} {
			name:=route+"-direct";if held{name=route+"-held"}
			t.Run(name,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
				a,c:=snapshotConn(t,p,b),snapshotConn(t,p,b)
				pid:=fixture.LockPID(t,p.Ctx,a)
				fixture.LockExec(t,p.Ctx,a,"CREATE TABLE sync_alter_atomic(id INT,v INT)",0)
				fixture.LockExec(t,p.Ctx,a,"INSERT INTO sync_alter_atomic VALUES(1,10),(2,20)",2)
				command:="ALTER TABLE sync_alter_atomic ADD COLUMN filled INT DEFAULT 7, ADD CONSTRAINT rejected CHECK (id<2 AND pg_export_snapshot() IS NOT NULL)"
				want:=fixture.ControlsReferenceDiagnostic("23514",`check constraint "rejected" of relation "sync_alter_atomic" is violated by some row`)
				want.Schema,want.Table,want.Constraint="public","sync_alter_atomic","rejected"
				switch route {
				case "default":
					command="ALTER TABLE sync_alter_atomic ADD COLUMN filled INT DEFAULT (1/0)"
					want=fixture.ControlsReferenceDiagnostic("22012","division by zero")
				case "index":
					fixture.LockExec(t,p.Ctx,a,"CREATE INDEX sync_alter_atomic_index ON sync_alter_atomic((id/(v-9)))",0)
					command="ALTER TABLE sync_alter_atomic ADD COLUMN filled INT DEFAULT 7, ALTER COLUMN v TYPE BIGINT USING 9::bigint"
					want=fixture.ControlsReferenceDiagnostic("22012","division by zero")
				}
				var statement *sql.Stmt
				if held {var err error;statement,err=a.PrepareContext(p.Ctx,command);if err!=nil{t.Fatal(err)};defer statement.Close()}
				fixture.LockExec(t,p.Ctx,a,"BEGIN",0);token:=syncExport(t,p,b,a,nil)
				fixture.LockExec(t,p.Ctx,a,"INSERT INTO sync_alter_atomic VALUES(9,90)",1)
				var err error;if held{_,err=statement.ExecContext(p.Ctx)}else{_,err=a.ExecContext(p.Ctx,command)}
				fixture.AssertDiagnostic(t,err,&want)
				syncAborted(t,p,c,token,pid);fixture.LockExec(t,p.Ctx,a,"ROLLBACK",0);syncExpired(t,p,c,token)
				fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT id,v FROM sync_alter_atomic ORDER BY id",[]string{"id","v"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(10)},{int64(2),int64(20)}}))
				fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT count(*) FROM pg_attribute WHERE attrelid='sync_alter_atomic'::regclass AND attname='filled'",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
				fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='sync_alter_atomic'::regclass AND conname='rejected'",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
			})})
		}
	}
}
