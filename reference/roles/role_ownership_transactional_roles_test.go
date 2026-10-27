package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipTransactionalRoleCandidates(t *testing.T) {
	ownershipTransactionCases(t, func(s *fixture.RoleMembershipScenario, route string) {
		ownershipSeed(s, "tx_candidate_authority", "own_middle")
		ownershipSeed(s, "tx_candidate_nil", "own_other")
		for _, q := range []string{
			"GRANT own_middle TO own_source WITH ADMIN TRUE",
			"GRANT own_middle TO own_actor WITH ADMIN TRUE",
			"GRANT own_middle TO own_reader WITH INHERIT TRUE GRANTED BY own_source",
			"GRANT own_middle TO own_downstream WITH INHERIT TRUE GRANTED BY own_actor",
			"COMMENT ON ROLE own_source IS 'committed'",
		} {
			s.Exec(q)
		}
		peer, err := s.Backend.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer peer.Close()
		first, second, nilStage := "DROP OWNED BY own_source", "DROP OWNED BY own_actor", "ALTER TABLE tx_candidate_nil OWNER TO own_target"
		state := func(e fixture.Executor, reader, downstream bool, comment string) {
			s.T.Helper()
			fixture.RunStep(s.T, s.P.Ctx, e, s.Record, fixture.Select("SELECT pg_has_role('own_reader','own_middle','MEMBER') AS reader_member,pg_has_role('own_reader','own_middle','USAGE') AS reader_usage,pg_has_role('own_reader','own_middle','SET') AS reader_set,pg_has_role('own_downstream','own_middle','MEMBER') AS downstream_member,pg_has_role('own_downstream','own_middle','USAGE') AS downstream_usage,pg_has_role('own_downstream','own_middle','SET') AS downstream_set", []string{"reader_member", "reader_usage", "reader_set", "downstream_member", "downstream_usage", "downstream_set"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{reader, reader, reader, downstream, downstream, downstream}}))
			fixture.RunStep(s.T, s.P.Ctx, e, s.Record, fixture.Select("SELECT shobj_description(oid,'pg_authid') AS comment FROM pg_roles WHERE rolname='own_source'", []string{"comment"}, []string{"TEXT"}, [][]any{{comment}}))
		}
		for _, commit := range []bool{false, true} {
			if route == "auto" && !commit {
				continue
			}
			x := ownershipBegin(s, route, first, second, nilStage)
			defer x.abort()
			x.exec("COMMENT ON ROLE own_source IS 'private predecessor'")
			x.exec(first)
			state(x.e, false, true, "private predecessor")
			x.exec("SET ROLE own_reader")
			denied := fixture.ImplicitError("SELECT id FROM tx_candidate_authority", fixture.Diagnostic{Code: "42501", Message: "permission denied for table tx_candidate_authority"})
			denied.SelectRows = true
			denied.ErrorStage = "execute"
			if route != "auto" {
				x.exec("SAVEPOINT denied")
			}
			x.run(denied)
			if route != "auto" {
				x.exec("ROLLBACK TO denied")
			}
			x.exec("RESET ROLE")
			x.exec(nilStage)
			state(x.e, false, true, "private predecessor") // nil candidate must not erase first cleanup.
			if route != "auto" {
				state(peer, true, true, "committed")
				x.exec("SAVEPOINT first_candidate")
			}
			x.exec(second)
			state(x.e, false, false, "private predecessor") // validate against first private root, not committed.
			if route != "auto" {
				x.exec("ROLLBACK TO first_candidate")
				state(x.e, false, true, "private predecessor")
				x.exec(second)
				x.exec("ROLLBACK TO first_candidate")
				state(x.e, false, true, "private predecessor")
				x.exec("RELEASE first_candidate")
				x.exec("SAVEPOINT failed_second")
				failure := fixture.ImplicitError("DROP OWNED BY own_actor,own_absent", fixture.Diagnostic{Code: "42704", Message: `role "own_absent" does not exist`})
				failure.ErrorStage = "execute"
				x.run(failure)
				x.run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
				x.exec("ROLLBACK TO failed_second")
				state(x.e, false, true, "private predecessor")
				x.exec(second)
			}
			x.end(commit)
			if commit {
				state(peer, false, false, "private predecessor")
			} else {
				state(peer, true, true, "committed")
			}
		}
		// Source endpoint membership is not removed by DROP OWNED.
		ownershipBool(s, "SELECT pg_has_role('own_source','own_middle','MEMBER')", true)
		ownershipBool(s, "SELECT pg_has_role('own_actor','own_middle','MEMBER')", true)
		s.Exec("DROP ROLE own_source")
		s.Exec("DROP ROLE own_actor")
	})
}
