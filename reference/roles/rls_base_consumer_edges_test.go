package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSBaseProposedReadConjunction(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("CREATE POLICY insert_allowed ON t FOR INSERT WITH CHECK(TRUE)")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		for _, q := range []string{"INSERT INTO t VALUES(1,99) ON CONFLICT(id) DO NOTHING", "INSERT INTO t VALUES(3,30) ON CONFLICT(id) DO NOTHING"} {
			rlsBaseProposalDenied(s, q)
		}
		// A missing SELECT qualifier does not move the demanded INSERT check
		// backwards into an empty INSERT SELECT source.
		s.Run(fixture.Exec("INSERT INTO t SELECT id,v FROM t", 0))
		s.Actor("rls_owner")
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}

func TestReferenceRLSBaseDefaultAndCoercion(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "rls_owner", func() { s.Exec("CREATE TABLE rls_default(id INT DEFAULT(10/(1-1)))") })
		s.Exec("GRANT INSERT ON rls_default TO rls_reader")
		s.Exec("ALTER TABLE rls_default ENABLE ROW LEVEL SECURITY")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		predefinedFailure(s, "INSERT INTO rls_default DEFAULT VALUES", fixture.Diagnostic{Code: "22012", Message: "division by zero"}, false, "execute")
		predefinedFailure(s, "INSERT INTO t VALUES(10/(1-1),30)", fixture.Diagnostic{Code: "22012", Message: "division by zero"}, false, "execute")
		predefinedFailure(s, "INSERT INTO t(id) VALUES('bad')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "26"}, false, "prepare")
		s.Actor("rls_owner")
		predefinedSelect(s, "SELECT id FROM rls_default", []string{"id"}, []string{"INT4"}, nil)
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}

func TestReferenceRLSBaseNestedWritablePrincipals(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "rls_owner", func() {
			s.Exec("CREATE VIEW rls_inner_owner AS SELECT id,v FROM t")
			s.Exec("CREATE VIEW rls_outer_owner AS SELECT id,v FROM rls_inner_owner")
			s.Exec("CREATE VIEW rls_inner_invoker WITH(security_invoker=true) AS SELECT id,v FROM t")
			s.Exec("CREATE VIEW rls_outer_invoker AS SELECT id,v FROM rls_inner_invoker")
			s.Exec("CREATE VIEW rls_identity AS SELECT id,current_user AS actor,session_user AS session_actor FROM t")
		})
		s.Exec("GRANT SELECT,INSERT,UPDATE,DELETE ON rls_inner_owner,rls_outer_owner,rls_inner_invoker,rls_outer_invoker TO rls_reader")
		s.Exec("GRANT SELECT ON rls_identity TO rls_reader")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		predefinedSelect(s, "SELECT id,actor,session_actor FROM rls_identity ORDER BY id", []string{"id", "actor", "session_actor"}, []string{"INT4", "NAME", "NAME"}, [][]any{{int64(1), "rls_reader", "rls_reader"}, {int64(2), "rls_reader", "rls_reader"}})
		s.Run(fixture.Exec("INSERT INTO rls_outer_owner VALUES(3,30)", 1))
		s.Run(fixture.Exec("UPDATE rls_outer_owner SET v=31 WHERE id=3", 1))
		s.Run(fixture.Exec("DELETE FROM rls_outer_owner WHERE id=3", 1))
		rlsBaseProposalDenied(s, "INSERT INTO rls_outer_invoker VALUES(3,30)")
		s.Run(fixture.Exec("UPDATE rls_outer_invoker SET v=31 WHERE id=1", 0))
		s.Run(fixture.Exec("DELETE FROM rls_outer_invoker WHERE id=1", 0))
		s.Actor("rls_owner")
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}
