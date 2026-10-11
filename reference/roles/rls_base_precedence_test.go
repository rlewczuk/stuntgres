package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSBaseProposalPrecedence(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("CREATE TABLE rls_stored(id INT,g INT GENERATED ALWAYS AS(10/(id-id)) STORED)")
		if s.backend.PG {
			s.exec("CREATE TABLE rls_virtual(id INT,g INT GENERATED ALWAYS AS(10/(id-id)) VIRTUAL)")
		} else {
			predefinedFailure(s, "CREATE TABLE rls_virtual(id INT,g INT GENERATED ALWAYS AS(10/(id-id)) VIRTUAL)", fixture.Diagnostic{Code: "0A000", Message: "virtual generated columns are unsupported"}, false, "execute")
			predefinedSelect(s, "SELECT tablename FROM pg_tables WHERE schemaname='public' AND tablename='rls_virtual'", []string{"tablename"}, []string{"NAME"}, nil)
		}
		s.exec("CREATE TABLE rls_constrained(id INT NOT NULL CHECK(id>0))")
		s.exec("CREATE TABLE rls_route(id INT) PARTITION BY RANGE(id)")
		s.exec("CREATE TABLE rls_route_leaf PARTITION OF rls_route FOR VALUES FROM(0) TO(10)")
		for _, table := range []string{"rls_stored", "rls_virtual", "rls_constrained", "rls_route"} {
			if table == "rls_virtual" && !s.backend.PG {
				continue
			}
			s.exec("GRANT INSERT,SELECT ON " + table + " TO rls_reader")
			s.exec("ALTER TABLE " + table + " ENABLE ROW LEVEL SECURITY")
		}
		s.actor("rls_reader")
		predefinedFailure(s, "INSERT INTO rls_stored(id) VALUES(1)", fixture.Diagnostic{Code: "22012", Message: "division by zero"}, false, "execute")
		if s.backend.PG {
			predefinedDenied(s, "INSERT INTO rls_virtual(id) VALUES(1)", `new row violates row-level security policy for table "rls_virtual"`, false)
		}
		for _, q := range []string{"INSERT INTO rls_constrained VALUES(NULL)", "INSERT INTO rls_constrained VALUES(-1)"} {
			predefinedDenied(s, q, `new row violates row-level security policy for table "rls_constrained"`, false)
		}
		predefinedFailure(s, "INSERT INTO rls_route VALUES(99)", fixture.Diagnostic{Code: "23514", Message: `no partition of relation "rls_route" found for row`, Schema: "public", Table: "rls_route"}, false, "execute")
		predefinedDenied(s, "INSERT INTO rls_route VALUES(7)", `new row violates row-level security policy for table "rls_route"`, false)
		s.actor("rls_owner")
		s.exec("GRANT INSERT,SELECT ON rls_route TO rls_bypass")
		s.actor("rls_bypass")
		predefinedFailure(s, "INSERT INTO rls_route VALUES(99)", fixture.Diagnostic{Code: "23514", Message: `no partition of relation "rls_route" found for row`, Detail: "Partition key of the failing row contains (id) = (99).", Schema: "public", Table: "rls_route"}, false, "execute")
		s.actor("rls_owner")
		s.exec("ALTER TABLE rls_route DISABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		predefinedFailure(s, "INSERT INTO rls_route VALUES(99)", fixture.Diagnostic{Code: "23514", Message: `no partition of relation "rls_route" found for row`, Detail: "Partition key of the failing row contains (id) = (99).", Schema: "public", Table: "rls_route"}, false, "execute")
		s.actor("rls_owner")
		for _, table := range []string{"rls_stored", "rls_virtual", "rls_constrained", "rls_route"} {
			if table == "rls_virtual" && !s.backend.PG {
				continue
			}
			predefinedSelect(s, "SELECT id FROM "+table, []string{"id"}, []string{"INT4"}, nil)
		}
	})
}

func TestReferenceRLSBaseExpressionUses(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.exec("CREATE POLICY select_only ON t FOR SELECT USING(TRUE)")
		s.actor("rls_reader")
		// Row marks require UPDATE as well as SELECT; an absent permissive
		// UPDATE qualifier is sufficient to deny the same input phase.
		predefinedSelect(s, "SELECT id FROM t FOR UPDATE", []string{"id"}, []string{"INT4"}, nil)
		predefinedSelect(s, "SELECT id FROM t FOR SHARE", []string{"id"}, []string{"INT4"}, nil)
		rlsBaseProposalDenied(s, "INSERT INTO t VALUES(3,30) ON CONFLICT(id) DO NOTHING")
		s.actor("rls_owner")
		s.exec("DROP POLICY select_only ON t")
		s.exec("CREATE POLICY update_only ON t FOR UPDATE USING(TRUE)")
		s.actor("rls_reader")
		s.run(fixture.Exec("UPDATE t SET v=99 WHERE id=1", 0))
		predefinedSelect(s, "UPDATE t SET v=99 RETURNING id", []string{"id"}, []string{"INT4"}, nil)
		s.actor("rls_owner")
		s.exec("DROP POLICY update_only ON t")
		s.exec("CREATE POLICY insert_only ON t FOR INSERT WITH CHECK(TRUE)")
		s.actor("rls_reader")
		s.run(fixture.Exec("INSERT INTO t SELECT id,v FROM t", 0))
		if s.backend.PG {
			s.run(fixture.Exec("INSERT INTO t VALUES(3,30)", 1))
		} else {
			predefinedFailure(s, "INSERT INTO t VALUES(3,30)", fixture.Diagnostic{Code: "0A000", Message: "row-security policy expression enforcement is unsupported"}, false, "execute")
		}
		s.actor("rls_owner")
		s.exec("DROP POLICY insert_only ON t")
		s.exec("CREATE POLICY all_fallback ON t USING(FALSE)")
		s.actor("rls_reader")
		if s.backend.PG {
			rlsBaseProposalDenied(s, "INSERT INTO t VALUES(4,40)")
		} else {
			predefinedFailure(s, "INSERT INTO t VALUES(4,40)", fixture.Diagnostic{Code: "0A000", Message: "row-security policy expression enforcement is unsupported"}, false, "execute")
		}
	})
}
