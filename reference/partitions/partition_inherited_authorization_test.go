package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionInheritedAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pi_owner_direct", "pi_owner_prepared"})
	partitionInheritedScenario(t, func(s *partitionInheritedCase) {
		role := "pi_owner_direct"
		if s.prepared {
			role = "pi_owner_prepared"
		}
		s.exec("CREATE ROLE "+role, 0)
		s.exec("GRANT USAGE,CREATE ON SCHEMA public TO "+role, 0)
		s.exec("CREATE TABLE pi_p(k INT,a INT CONSTRAINT pi_nn NOT NULL,b INT,CONSTRAINT pi_ck CHECK(b=1)) PARTITION BY RANGE(k)", 0)
		s.exec("CREATE TABLE pi_mid PARTITION OF pi_p DEFAULT PARTITION BY RANGE(k)", 0)
		s.exec("CREATE TABLE pi_lo PARTITION OF pi_mid DEFAULT", 0)
		old := s.oid("pi_lo", "pi_ck")
		s.t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := s.conn.ExecContext(ctx, "RESET ROLE"); err != nil {
				s.t.Error(err)
			}
		})
		q := "ALTER TABLE pi_p ADD CONSTRAINT pi_extra CHECK(a>0)"
		stmt, err := s.conn.PrepareContext(s.p.Ctx, q)
		if err != nil {
			s.t.Fatal(err)
		}
		defer stmt.Close()
		s.exec("SET ROLE "+role, 0)
		for _, action := range []string{"ADD CONSTRAINT pi_extra CHECK(a>0)", "ADD CONSTRAINT pi_other_nn NOT NULL a", "ALTER COLUMN a SET NOT NULL", "DROP CONSTRAINT pi_ck", "ALTER COLUMN a DROP NOT NULL"} {
			s.fail("ALTER TABLE pi_p "+action, fixture.Diagnostic{Code: "42501", Message: "must be owner of table pi_p"})
		}
		// A statement prepared while authorized must check the current owner.
		held := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "must be owner of table pi_p"})
		held.ErrorStage = "execute"
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: stmt, SQL: q}, s.recorder, held)
		s.exec("RESET ROLE", 0)
		s.sameOID("pi_lo", "pi_ck", old)
		for i, table := range []string{"pi_p", "pi_mid", "pi_lo"} {
			count := int64(1)
			if i == 0 {
				count = 0
			}
			s.facts(table, [][]any{partitionInheritedFact("pi_ck", "c", i == 0, count, "{3}"), partitionInheritedFact("pi_nn", "n", i == 0, count, "{2}")})
		}
		s.exec("ALTER TABLE pi_p OWNER TO "+role, 0)
		s.exec("SET ROLE "+role, 0)
		// Ownership of the virtual root alone must not authorize a fresh
		// descendant change. The exact failing relation is oracle-discovered.
		s.fail(q, fixture.Diagnostic{Code: "42501", Message: "must be owner of table pi_mid"})
		// Repeating SET on an existing root fact may stop before descendants.
		s.exec("ALTER TABLE ONLY pi_p ALTER COLUMN a SET NOT NULL", 0)
		s.exec("RESET ROLE", 0)
		s.facts("pi_p", [][]any{partitionInheritedFact("pi_ck", "c", true, 0, "{3}"), partitionInheritedFact("pi_nn", "n", true, 0, "{2}")})
		s.sameOID("pi_lo", "pi_ck", old)
	})
}
