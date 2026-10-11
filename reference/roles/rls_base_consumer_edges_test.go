package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSBaseProposedReadConjunction(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("CREATE POLICY insert_allowed ON t FOR INSERT WITH CHECK(TRUE)")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		for _, q := range []string{"INSERT INTO t VALUES(1,99) ON CONFLICT(id) DO NOTHING", "INSERT INTO t VALUES(3,30) ON CONFLICT(id) DO NOTHING"} {
			rlsBaseProposalDenied(s, q)
		}
		// A missing SELECT qualifier does not move the demanded INSERT check
		// backwards into an empty INSERT SELECT source.
		s.run(fixture.Exec("INSERT INTO t SELECT id,v FROM t", 0))
		s.actor("rls_owner")
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}

func TestReferenceRLSBaseDefaultAndCoercion(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("CREATE TABLE rls_default(id INT DEFAULT(10/(1-1)))")
		s.exec("GRANT INSERT ON rls_default TO rls_reader")
		s.exec("ALTER TABLE rls_default ENABLE ROW LEVEL SECURITY")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		predefinedFailure(s, "INSERT INTO rls_default DEFAULT VALUES", fixture.Diagnostic{Code: "22012", Message: "division by zero"}, false, "execute")
		predefinedFailure(s, "INSERT INTO t VALUES(10/(1-1),30)", fixture.Diagnostic{Code: "22012", Message: "division by zero"}, false, "execute")
		predefinedFailure(s, "INSERT INTO t(id) VALUES('bad')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "26"}, false, "prepare")
		s.actor("rls_owner")
		predefinedSelect(s, "SELECT id FROM rls_default", []string{"id"}, []string{"INT4"}, nil)
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}

func TestReferenceRLSBaseNestedWritablePrincipals(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("CREATE VIEW rls_inner_owner AS SELECT id,v FROM t")
		s.exec("CREATE VIEW rls_outer_owner AS SELECT id,v FROM rls_inner_owner")
		s.exec("CREATE VIEW rls_inner_invoker WITH(security_invoker=true) AS SELECT id,v FROM t")
		s.exec("CREATE VIEW rls_outer_invoker AS SELECT id,v FROM rls_inner_invoker")
		s.exec("CREATE VIEW rls_identity AS SELECT id,current_user AS actor,session_user AS session_actor FROM t")
		s.exec("GRANT SELECT,INSERT,UPDATE,DELETE ON rls_inner_owner,rls_outer_owner,rls_inner_invoker,rls_outer_invoker TO rls_reader")
		s.exec("GRANT SELECT ON rls_identity TO rls_reader")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		predefinedSelect(s, "SELECT id,actor,session_actor FROM rls_identity ORDER BY id", []string{"id", "actor", "session_actor"}, []string{"INT4", "NAME", "NAME"}, [][]any{{int64(1), "rls_reader", "rls_reader"}, {int64(2), "rls_reader", "rls_reader"}})
		s.run(fixture.Exec("INSERT INTO rls_outer_owner VALUES(3,30)", 1))
		s.run(fixture.Exec("UPDATE rls_outer_owner SET v=31 WHERE id=3", 1))
		s.run(fixture.Exec("DELETE FROM rls_outer_owner WHERE id=3", 1))
		rlsBaseProposalDenied(s, "INSERT INTO rls_outer_invoker VALUES(3,30)")
		s.run(fixture.Exec("UPDATE rls_outer_invoker SET v=31 WHERE id=1", 0))
		s.run(fixture.Exec("DELETE FROM rls_outer_invoker WHERE id=1", 0))
		s.actor("rls_owner")
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}
