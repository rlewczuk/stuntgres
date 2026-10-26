package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoReferentialConstraintsVisibility(t *testing.T) {
	ircScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,db string) {
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,r,s)}
		ircSeed(run)
		for _,role:=range ircRoles { q:="CREATE ROLE "+role; if role=="irc_noinherit" {q+=" NOINHERIT"}; run(fixture.Exec(q,0)) }
		for _,q:=range []string{
			"ALTER TABLE irc_scope.child OWNER TO irc_owner",
			"REVOKE ALL ON irc_scope.child FROM irc_owner",
			"GRANT irc_owner TO irc_member WITH INHERIT TRUE, SET TRUE",
			"GRANT irc_owner TO irc_noinherit WITH INHERIT FALSE, SET TRUE",
			"REVOKE ALL ON SCHEMA irc_scope FROM PUBLIC",
		} {run(fixture.Exec(q,0))}
		where:="constraint_schema='irc_scope'"
		row:=ircRow(db,"irc_scope","child_fk","irc_scope","parent_pk","NONE","NO ACTION","NO ACTION")
		held:=ircHold(t,p,c,ircProjection(b.PG,where,nil).Query)
		check:=func(want ...[]any){ircCheck(t,p,c,r,held,ircProjection(b.PG,where,want))}
		actor:=func(role string,want ...[]any){run(fixture.Exec("SET SESSION AUTHORIZATION "+role,0));check(want...);run(fixture.Exec("RESET SESSION AUTHORIZATION",0))}
		check(row)
		actor("irc_owner",row); actor("irc_member",row); actor("irc_noinherit"); actor("irc_reader")
		run(fixture.Exec("SET SESSION AUTHORIZATION irc_noinherit",0));check()
		run(fixture.Exec("SET ROLE irc_owner",0));check(row)
		run(fixture.Exec("RESET ROLE",0));check()
		run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		admin,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer admin.Close()
		adminRun:=func(q string){fixture.RunStep(t,p.Ctx,admin,b.Recorder,fixture.Exec(q,0))}
		run(fixture.Exec("SET SESSION AUTHORIZATION irc_reader",0))
		for _,privilege:=range []string{"SELECT","MAINTAIN","INSERT","UPDATE","DELETE","TRUNCATE","REFERENCES","TRIGGER"} {
			adminRun("GRANT "+privilege+" ON irc_scope.child TO irc_reader")
			if privilege=="SELECT" || privilege=="MAINTAIN" {check()} else {check(row)}
			adminRun("REVOKE "+privilege+" ON irc_scope.child FROM irc_reader");check()
		}
		for _,privilege:=range []string{"SELECT","INSERT","UPDATE","REFERENCES"} {
			adminRun("GRANT "+privilege+"(extra) ON irc_scope.child TO irc_reader")
			if privilege=="SELECT" {check()} else {check(row)}
			adminRun("REVOKE "+privilege+"(extra) ON irc_scope.child FROM irc_reader");check()
		}
		adminRun("GRANT ALL ON irc_scope.parent TO irc_reader");check()
		adminRun("REVOKE ALL ON irc_scope.parent FROM irc_reader")
		adminRun("GRANT USAGE ON SCHEMA irc_scope TO irc_reader");check()
		adminRun("REVOKE USAGE ON SCHEMA irc_scope FROM irc_reader")
		adminRun("GRANT UPDATE ON irc_scope.child TO PUBLIC");check(row)
		adminRun("REVOKE UPDATE ON irc_scope.child FROM PUBLIC");check()
		run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		adminRun("GRANT irc_group TO irc_reader WITH INHERIT TRUE, SET TRUE")
		adminRun("GRANT UPDATE ON irc_scope.child TO irc_group");actor("irc_reader",row)
		adminRun("REVOKE UPDATE ON irc_scope.child FROM irc_group");actor("irc_reader")
		for _,q:=range []string{"CREATE SCHEMA irc_private","REVOKE ALL ON SCHEMA irc_private FROM PUBLIC","ALTER TABLE irc_scope.child SET SCHEMA irc_private","GRANT UPDATE ON irc_private.child TO irc_reader"}{run(fixture.Exec(q,0))}
		run(fixture.Exec("SET SESSION AUTHORIZATION irc_reader",0))
		run(ircProjection(b.PG,"constraint_schema='irc_private'",[][]any{ircRow(db,"irc_private","child_fk","irc_scope","parent_pk","NONE","NO ACTION","NO ACTION")}))
		d:=fixture.ControlsReferenceDiagnostic("42501","permission denied for schema irc_private");d.Position="15"
		s:=fixture.ErrorDiagnostic("SELECT * FROM irc_private.child",d);s.SelectRows=true;s.ErrorStage="execute";run(s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		// A different session's temporary namespace is not a visibility filter.
		temp,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer temp.Close()
		for _,q:=range []string{"CREATE TEMP TABLE irc_temp_parent(id int PRIMARY KEY)","CREATE TEMP TABLE irc_temp_child(pid int CONSTRAINT irc_temp_fk REFERENCES irc_temp_parent)","GRANT UPDATE ON irc_temp_child TO irc_reader"}{fixture.RunStep(t,p.Ctx,temp,b.Recorder,fixture.Exec(q,0))}
		var ns string
		if err:=temp.QueryRowContext(p.Ctx,"SELECT nspname FROM pg_catalog.pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&ns);err!=nil{t.Fatal(err)}
		run(fixture.Exec("SET SESSION AUTHORIZATION irc_reader",0))
		run(ircProjection(b.PG,"constraint_schema=$1 AND constraint_name='irc_temp_fk'",[][]any{ircRow(db,ns,"irc_temp_fk",ns,"irc_temp_parent_pkey","NONE","NO ACTION","NO ACTION")},ns))
		run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		if _,err:=temp.ExecContext(p.Ctx,"DROP TABLE irc_temp_child,irc_temp_parent");err!=nil{t.Fatal(err)}
	})
}
