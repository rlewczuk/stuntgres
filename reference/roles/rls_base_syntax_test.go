package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSBaseSyntax(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		for _, action := range []string{"ENABLE", "DISABLE", "FORCE", "NO FORCE"} {
			s.ClearNotices()
			s.Exec("ALTER TABLE IF EXISTS rls_absent " + action + " ROW LEVEL SECURITY")
			s.Notice("NOTICE", "00000", `relation "rls_absent" does not exist, skipping`)
		}
		predefinedFailure(s, "ALTER TABLE t ENABLE ROW LEVEL SECURITY garbage", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "garbage"`, Position: "41"}, false, "prepare")
		predefinedFailure(s, "ALTER TABLE t ENABLE ROW SECURITY", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "SECURITY"`, Position: "26"}, false, "prepare")
		rlsBaseEnabled(s, false)
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "rls_owner", func() { s.Run(fixture.Exec("CREATE MATERIALIZED VIEW rls_kind_mv AS SELECT id FROM t", 2)) })
		predefinedFailure(s, "ALTER TABLE rls_kind_mv ENABLE ROW LEVEL SECURITY", fixture.Diagnostic{Code: "42809", Message: `ALTER action ENABLE ROW SECURITY cannot be performed on relation "rls_kind_mv"`, Detail: "This operation is not supported for materialized views."}, false, "execute")
		predefinedFailure(s, "ALTER TABLE t_pkey FORCE ROW LEVEL SECURITY", fixture.Diagnostic{Code: "42809", Message: `ALTER action FORCE ROW SECURITY cannot be performed on relation "t_pkey"`, Detail: "This operation is not supported for indexes."}, false, "execute")
		s.Exec("ALTER TABLE IF EXISTS t ENABLE ROW LEVEL SECURITY")
		rlsBaseEnabled(s, true)
		s.Actor("rls_reader")
		rlsBaseRows(s, nil)
	})
}
