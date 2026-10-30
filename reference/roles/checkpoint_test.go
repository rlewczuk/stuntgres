package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var checkpointDenied = fixture.Diagnostic{Code:"42501", Message:"permission denied to execute CHECKPOINT command", Detail:`Only roles with privileges of the "pg_checkpoint" role may execute this command.`}
var checkpointPhysical = fixture.Diagnostic{Code:"0A000", Message:"CHECKPOINT physical WAL and fsync operations are unsupported in embedded mode"}
var checkpointAborted = fixture.Diagnostic{Code:"25P02", Message:"current transaction is aborted, commands ignored until end of transaction block"}

func checkpointRun(s *fixture.RoleMembershipScenario, allowed bool) {
	s.T.Helper()
	if !allowed { predefinedFailure(s, "CHECKPOINT", checkpointDenied, false, "execute"); return }
	if s.Backend.PG { s.Exec("CHECKPOINT") } else { predefinedFailure(s, "CHECKPOINT", checkpointPhysical, false, "execute") }
}

var checkpointMalformed = []struct{ sql, token, pos string }{
	{"CHECKPOINT x", "x", "12"}, {"CHECKPOINT (fast)", "(", "12"},
	{"CHECKPOINT IMMEDIATE", "IMMEDIATE", "12"}, {"CHECKPOINT FORCE", "FORCE", "12"},
	{"CHECKPOINT $1", "$1", "12"}, {"CHECKPOINT 1", "1", "12"},
	{"EXPLAIN CHECKPOINT", "CHECKPOINT", "9"}, {"PREPARE p AS CHECKPOINT", "CHECKPOINT", "14"},
	{"CHECKPOINT; SELECT )", ")", "20"},
}

func TestReferenceCheckpointSyntax(t *testing.T) {
	predefinedCases(t, []string{"cp_actor"}, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		s.Exec("CREATE TABLE checkpoint(checkpoint INT)")
		s.Run(fixture.Exec("INSERT INTO checkpoint VALUES(7)", 1))
		predefinedSelect(s, "SELECT checkpoint FROM checkpoint", []string{"checkpoint"}, []string{"INT4"}, [][]any{{int64(7)}})
		s.Exec(`CREATE TABLE "CHECKPOINT"("CHECKPOINT" INT)`)
		for _, actor := range []string{"", "cp_actor"} {
			s.Actor(actor)
			for _, q := range []string{"CHECKPOINT", "cHeCkPoInT", " /* before */ CHECKPOINT /* after */; ;"} {
				if actor == "" && s.Backend.PG { s.Exec(q) } else {
					d := checkpointDenied
					if actor == "" { d = checkpointPhysical }
					predefinedFailure(s, q, d, false, "execute")
				}
			}
			for _, tc := range checkpointMalformed {
				predefinedFailure(s, tc.sql, fixture.Diagnostic{Code:"42601", Message:`syntax error at or near "`+tc.token+`"`, Position:tc.pos}, false, "prepare")
			}
			if s.Prepared { predefinedFailure(s, "CHECKPOINT; SELECT 1", fixture.Diagnostic{Code:"42601", Message:"cannot insert multiple commands into a prepared statement"}, false, "prepare") }
		}
		s.Actor("")
		predefinedFailure(s, "INSERT INTO checkpoint VALUES(8); CHECKPOINT x", fixture.Diagnostic{Code:"42601", Message:`syntax error at or near "x"`, Position:"46"}, false, "prepare")
		predefinedSelect(s, "SELECT checkpoint FROM checkpoint", []string{"checkpoint"}, []string{"INT4"}, [][]any{{int64(7)}})
	})
}

func TestReferenceCheckpointAuthorization(t *testing.T) {
	predefinedCases(t, []string{"cp_actor", "cp_middle", "cp_top", "cp_super"}, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		checkpointRun(s, true)
		// Query also exposes an empty descriptor, never a fabricated result row.
		if s.Backend.PG {
			s.Run(fixture.Select("CHECKPOINT", nil, nil, nil))
		} else {
			predefinedFailure(s, "CHECKPOINT", checkpointPhysical, true, "execute")
		}
		s.Exec("SET ROLE cp_actor")
		checkpointRun(s, false)
		s.Exec("RESET ROLE")
		s.Exec("SET ROLE pg_checkpoint")
		checkpointRun(s, true)
		s.Exec("RESET ROLE")
		s.Actor("cp_actor")
		checkpointRun(s, false)
		s.Actor("")
		s.Exec("GRANT pg_checkpoint TO cp_actor")
		s.Actor("cp_actor")
		checkpointRun(s, true)
		s.Actor("")
		s.Exec("REVOKE pg_checkpoint FROM cp_actor")
		s.Exec("ALTER ROLE cp_actor NOINHERIT")
		s.Exec("GRANT pg_checkpoint TO cp_actor WITH INHERIT FALSE, SET TRUE")
		s.Actor("cp_actor")
		checkpointRun(s, false)
		s.Exec("SET ROLE pg_checkpoint")
		checkpointRun(s, true)
		s.Actor("")
		s.Exec("GRANT pg_checkpoint TO cp_actor WITH INHERIT TRUE, SET FALSE")
		s.Actor("cp_actor")
		checkpointRun(s, true)
		s.Actor("")
		s.Exec("GRANT pg_checkpoint TO cp_actor WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		s.Actor("cp_actor")
		checkpointRun(s, false)
		s.Actor("")
		s.Exec("REVOKE pg_checkpoint FROM cp_actor")
		s.Exec("GRANT pg_checkpoint TO cp_top WITH INHERIT TRUE")
		s.Exec("GRANT cp_top TO cp_middle WITH INHERIT TRUE")
		s.Exec("GRANT cp_middle TO cp_actor WITH INHERIT TRUE")
		s.Actor("cp_actor")
		checkpointRun(s, true)
		s.Actor("")
		s.Exec("GRANT cp_top TO cp_middle WITH INHERIT FALSE")
		s.Actor("cp_actor")
		checkpointRun(s, false)
		s.Actor("")
		s.Exec("ALTER ROLE cp_super SUPERUSER")
		s.Exec("GRANT cp_super TO cp_actor WITH INHERIT TRUE")
		s.Actor("cp_actor")
		checkpointRun(s, false)
	})
}

func TestReferenceCheckpointPreparedAuthority(t *testing.T) {
	predefinedCases(t, []string{"cp_actor"}, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		s.Actor("cp_actor")
		held := predefinedHeld(s, "CHECKPOINT")
		run := func(allowed bool) {
			s.T.Helper()
			step := fixture.Exec("CHECKPOINT", 0)
			if !allowed { d:=checkpointDenied; d.Severity="ERROR"; d.SeverityUnlocalized="ERROR"; step=fixture.ErrorDiagnostic("CHECKPOINT", d) } else if !s.Backend.PG { d:=checkpointPhysical; d.Severity="ERROR"; d.SeverityUnlocalized="ERROR"; step=fixture.ErrorDiagnostic("CHECKPOINT", d) }
			fixture.RunStep(s.T, s.P.Ctx, held, s.Record, step)
		}
		run(false)
		for _, grant := range []bool{true, false, true} {
			s.Actor("")
			if grant { s.Exec("GRANT pg_checkpoint TO cp_actor") } else { s.Exec("REVOKE pg_checkpoint FROM cp_actor") }
			s.Actor("cp_actor")
			run(grant)
		}
		s.Actor("")
		s.Exec("SET ROLE cp_actor")
		predefinedSelect(s, "SELECT current_user,session_user", []string{"current_user","session_user"}, []string{"NAME","NAME"}, [][]any{{"cp_actor",s.Bootstrap}})
		run(true)
		s.Exec("SET ROLE pg_checkpoint")
		run(true)
		s.Exec("RESET ROLE")
		run(true)
		s.Exec("REVOKE pg_checkpoint FROM cp_actor")
		s.Exec("SET ROLE cp_actor")
		run(false)
		s.Exec("RESET ROLE")
		run(true)
	})
}
