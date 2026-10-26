package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoReferentialConstraintsDiagnostics(t *testing.T) {
	ircScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,db string) {
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,r,s)}
		ircSeed(run)
		want:=[][]any{ircRow(db,"irc_scope","child_fk","irc_scope","parent_pk","NONE","NO ACTION","NO ACTION")}
		d:=fixture.ControlsReferenceDiagnostic("42703",`column "missing" does not exist`);d.Position="8"
		for _,prepared:=range []bool{false,true} {
			s:=fixture.ErrorDiagnostic("SELECT missing FROM information_schema.referential_constraints",d)
			s.SelectRows,s.Prepared,s.ErrorStage=true,prepared,"execute";if prepared{s.ErrorStage="prepare"};run(s)
		}
		for _,tc:=range []struct{query,message,action,command string}{
			{"INSERT INTO information_schema.referential_constraints(constraint_name) VALUES('bad')",`cannot insert into view "referential_constraints"`,"inserting into","INSERT"},
			{"UPDATE information_schema.referential_constraints SET constraint_name='bad'",`cannot update view "referential_constraints"`,"updating","UPDATE"},
			{"DELETE FROM information_schema.referential_constraints",`cannot delete from view "referential_constraints"`,"deleting from","DELETE"},
		} {
			d:=fixture.ControlsReferenceDiagnostic("55000",tc.message)
			d.Detail="Views that do not select from a single table or view are not automatically updatable."
			d.Hint="To enable "+tc.action+" the view, provide an INSTEAD OF "+tc.command+" trigger or an unconditional ON "+tc.command+" DO INSTEAD rule."
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.query, d)
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				run(ircProjection(b.PG, "constraint_schema='irc_scope'", want))
			}
		}
		run(fixture.Exec("CREATE TABLE irc_scope.nonunique(id int)",0))
		d=fixture.ForeignKeyError("42830",`there is no unique constraint matching given keys for referenced table "nonunique"`,"","","")
		for _,prepared:=range []bool{false,true} {
			run(fixture.ForeignKeyFail("CREATE TABLE irc_scope.bad(pid int REFERENCES irc_scope.nonunique(id))",d,prepared))
			run(ircProjection(b.PG,"constraint_schema='irc_scope'",want))
			run(fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relnamespace='irc_scope'::regnamespace AND relname='bad'",[]string{"relname"},[]string{"NAME"},nil))
		}
	})
}
