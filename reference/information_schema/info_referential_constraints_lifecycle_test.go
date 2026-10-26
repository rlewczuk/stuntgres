package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoReferentialConstraintsLifecycle(t *testing.T) {
	ircScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,db string) {
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,r,s)}
		for _,q:=range []string{"CREATE SCHEMA irc_scope","CREATE SCHEMA irc_moved","CREATE TABLE irc_scope.parent(id int CONSTRAINT parent_pk PRIMARY KEY)","CREATE TABLE irc_scope.child(pid int)"}{run(fixture.Exec(q,0))}
		where:="constraint_schema IN ('irc_scope','irc_moved')"
		held:=ircHold(t,p,c,ircProjection(b.PG,where,nil).Query)
		check:=func(want ...[]any){ircCheck(t,p,c,r,held,ircProjection(b.PG,where,want))}
		check()
		run(fixture.Exec("ALTER TABLE irc_scope.child ADD CONSTRAINT child_fk FOREIGN KEY(pid) REFERENCES irc_scope.parent",0))
		row:=ircRow(db,"irc_scope","child_fk","irc_scope","parent_pk","NONE","NO ACTION","NO ACTION");check(row)
		run(fixture.Exec("ALTER TABLE irc_scope.child RENAME CONSTRAINT child_fk TO renamed_fk",0));row[2]="renamed_fk";check(row)
		run(fixture.Exec("ALTER TABLE irc_scope.parent RENAME CONSTRAINT parent_pk TO renamed_pk",0));row[5]="renamed_pk";check(row)
		run(fixture.Exec("ALTER TABLE irc_scope.parent SET SCHEMA irc_moved",0));row[4]="irc_moved";check(row)
		run(fixture.Exec("ALTER TABLE irc_scope.child SET SCHEMA irc_moved",0));row[1]="irc_moved";check(row)
		run(fixture.Exec("BEGIN",0));run(ircProjection(b.PG,where,[][]any{row}));run(fixture.Exec("ROLLBACK",0));check(row)
		// The creating transaction sees its private FK; a peer retains the
		// committed bag, and rollback removes the private definition entirely.
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE TABLE irc_scope.pending(pid int CONSTRAINT pending_fk REFERENCES irc_moved.parent)", 0))
		pending := ircRow(db, "irc_scope", "pending_fk", "irc_moved", "renamed_pk", "NONE", "NO ACTION", "NO ACTION")
		run(ircProjection(b.PG, where, [][]any{row, pending}))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, ircProjection(b.PG, where, [][]any{row}))
		if err := peer.Close(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("ROLLBACK",0));check(row)
		run(fixture.Exec("CREATE TABLE irc_scope.nonunique(id int)",0))
		d:=fixture.ForeignKeyError("42830",`there is no unique constraint matching given keys for referenced table "nonunique"`,"","","")
		run(fixture.ForeignKeyFail("ALTER TABLE irc_moved.child ADD CONSTRAINT bad_fk FOREIGN KEY(pid) REFERENCES irc_scope.nonunique(id)",d,false));check(row)
		var retained *sql.Rows
		if !b.PG {var err error;retained,err=b.DB.QueryContext(p.Ctx,ircProjection(false,where,nil).Query);if err!=nil{t.Fatal(err)};defer retained.Close()}
		run(fixture.Exec("ALTER TABLE irc_moved.child DROP CONSTRAINT renamed_fk",0));check()
		if retained!=nil {ircRetained(t,retained,[][]any{row})}
		run(fixture.Exec("ALTER TABLE irc_moved.child ADD CONSTRAINT again_fk FOREIGN KEY(pid) REFERENCES irc_moved.parent",0));row[2]="again_fk";check(row)
		run(fixture.Exec("DROP TABLE irc_moved.child",0));check()
	})
}
