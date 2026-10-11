package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSBaseDefaultDenyAndInputs(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		rlsBaseRows(s, nil)
		predefinedSelect(s, "SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
		predefinedSelect(s, "SELECT 7 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}})
		predefinedSelect(s, "SELECT EXISTS(SELECT 1 FROM t) AS found, (SELECT id FROM t) AS scalar", []string{"found", "scalar"}, []string{"BOOL", "INT4"}, [][]any{{false, nil}})
		predefinedSelect(s, "SELECT x.id,t.id AS hidden FROM (VALUES(7)) AS x(id) LEFT JOIN t ON true", []string{"id", "hidden"}, []string{"INT4", "INT4"}, [][]any{{int64(7), nil}})
		predefinedSelect(s, "SELECT id FROM t WHERE 10/(id-id)>0", []string{"id"}, []string{"INT4"}, nil)
		if s.backend.PG {
			predefinedFailure(s, "SELECT id FROM t WHERE 1/0>0", fixture.Diagnostic{Code: "22012", Message: "division by zero"}, true, "execute")
		} else {
			// Native ordinary arithmetic remains row-owned; no demanded row
			// means no arithmetic evaluation, unlike PostgreSQL planner folding.
			predefinedSelect(s, "SELECT id FROM t WHERE 1/0>0", []string{"id"}, []string{"INT4"}, nil)
		}
		predefinedSelect(s, "SELECT id,count(*) FROM t GROUP BY id", []string{"id", "count"}, []string{"INT4", "INT8"}, nil)
		predefinedSelect(s, "SELECT id,row_number() OVER(ORDER BY id) AS n FROM t", []string{"id", "n"}, []string{"INT4", "INT8"}, nil)
		predefinedSelect(s, "SELECT id FROM t UNION ALL SELECT 7 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		s.run(fixture.Exec("CREATE TABLE rls_empty AS SELECT id FROM t", 0))
		predefinedSelect(s, "SELECT id FROM rls_empty", []string{"id"}, []string{"INT4"}, nil)
	})
}

func TestReferenceRLSBaseWrites(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("CREATE TABLE rls_src(id INT,v INT)")
		s.run(fixture.Exec("INSERT INTO rls_src VALUES(1,10)", 1))
		s.exec("GRANT SELECT ON rls_src TO rls_reader")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		s.run(fixture.Exec("UPDATE t SET v=100/(id-id)", 0))
		s.run(fixture.Exec("DELETE FROM t WHERE 100/(id-id)>0", 0))
		predefinedSelect(s, "UPDATE t SET v=99 RETURNING id", []string{"id"}, []string{"INT4"}, nil)
		predefinedSelect(s, "DELETE FROM t RETURNING id", []string{"id"}, []string{"INT4"}, nil)
		s.run(fixture.Exec("UPDATE t SET v=rls_src.v FROM rls_src WHERE t.id=rls_src.id", 0))
		s.run(fixture.Exec("DELETE FROM t USING rls_src WHERE t.id=rls_src.id", 0))
		for _, q := range []string{
			"INSERT INTO t VALUES(3,30)",
			"INSERT INTO t(id) VALUES(3)",
			"INSERT INTO t DEFAULT VALUES",
			"INSERT INTO t VALUES(3,30),(4,40)",
			"INSERT INTO t SELECT id,v FROM rls_src",
			"INSERT INTO t VALUES(1,99) ON CONFLICT DO NOTHING",
			"INSERT INTO t VALUES(1,99) ON CONFLICT(id) DO NOTHING",
			"INSERT INTO t VALUES(3,30) ON CONFLICT DO NOTHING",
		} {
			rlsBaseProposalDenied(s, q)
		}
		s.run(fixture.Exec("INSERT INTO t SELECT id,v FROM rls_src WHERE false", 0))
		rlsBaseProposalDenied(s, "MERGE INTO t USING rls_src s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v)")
		s.run(fixture.Exec("MERGE INTO t USING rls_src s ON t.id=s.id WHEN NOT MATCHED AND false THEN INSERT VALUES(s.id,s.v)", 0))
		s.actor("rls_owner")
		s.exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		s.exec("ALTER TABLE rls_src ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		s.run(fixture.Exec("UPDATE t SET v=rls_src.v FROM rls_src WHERE t.id=rls_src.id", 0))
		s.run(fixture.Exec("DELETE FROM t USING rls_src WHERE t.id=rls_src.id", 0))
		s.run(fixture.Exec("INSERT INTO t SELECT id,v FROM rls_src", 0))
		s.run(fixture.Exec("MERGE INTO t USING rls_src s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v)", 0))
		s.actor("rls_owner")
		predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
	})
}

func TestReferenceRLSBaseApplicability(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.exec("CREATE POLICY unrelated ON t TO rls_other USING(TRUE)")
		s.actor("rls_reader")
		rlsBaseRows(s, nil)
		s.actor("rls_owner")
		s.exec("DROP POLICY unrelated ON t")
		s.exec("CREATE POLICY command_only ON t FOR DELETE USING(TRUE)")
		s.actor("rls_reader")
		rlsBaseRows(s, nil)
		s.actor("rls_owner")
		s.exec("DROP POLICY command_only ON t")
		s.exec("CREATE POLICY restrict_only ON t AS RESTRICTIVE USING(10/(id-id)>0)")
		s.actor("rls_reader")
		rlsBaseRows(s, nil)
		rlsBaseProposalDenied(s, "INSERT INTO t VALUES(3,30)")
		s.actor("rls_owner")
		s.exec("DROP POLICY restrict_only ON t")
		s.exec("CREATE POLICY omitted ON t")
		s.actor("rls_reader")
		rlsBaseRows(s, nil)
		rlsBaseProposalDenied(s, "INSERT INTO t VALUES(3,30)")
		s.actor("rls_owner")
		s.exec("DROP POLICY omitted ON t")
		for _, tc := range []struct {
			expression string
			rows       [][]any
		}{
			{"TRUE", rlsBaseSeedRows()}, {"FALSE", nil}, {"NULL", nil},
		} {
			s.exec("CREATE POLICY expression ON t FOR SELECT USING(" + tc.expression + ")")
			s.actor("rls_reader")
			rlsBaseExpression(s, "SELECT id FROM t ORDER BY id", tc.rows)
			// UPDATE has no grant-producing qualifier: the same existing-row
			// conjunction is provably false even with an applicable SELECT policy.
			s.run(fixture.Exec("UPDATE t SET v=99 WHERE id=1", 0))
			s.actor("rls_owner")
			s.exec("DROP POLICY expression ON t")
		}
		s.exec("CREATE POLICY inherited ON t TO rls_other USING(TRUE)")
		s.actor("")
		s.exec("GRANT rls_other TO rls_reader WITH INHERIT FALSE, SET TRUE")
		s.actor("rls_reader")
		rlsBaseRows(s, nil)
		s.actor("")
		s.exec("GRANT rls_other TO rls_reader WITH INHERIT TRUE")
		s.actor("rls_reader")
		rlsBaseExpression(s, "SELECT id FROM t ORDER BY id", rlsBaseSeedRows())
	})
}

func TestReferenceRLSBasePredefinedReadAllData(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.actor("")
		s.exec("CREATE SCHEMA pdef_rls AUTHORIZATION rls_owner")
		s.actor("rls_owner")
		s.exec("CREATE TABLE pdef_rls.private_t(id INT)")
		s.run(fixture.Exec("INSERT INTO pdef_rls.private_t VALUES(1),(2)", 2))
		s.actor("")
		s.exec("GRANT pg_read_all_data,pg_write_all_data TO rls_other")
		s.actor("rls_other")
		predefinedSelect(s, "SELECT id FROM pdef_rls.private_t ORDER BY id", []string{"id"}, []string{"INT4"}, rlsBaseSeedRows())
		s.actor("rls_owner")
		s.exec("ALTER TABLE pdef_rls.private_t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_other")
		predefinedSelect(s, "SELECT id FROM pdef_rls.private_t", []string{"id"}, []string{"INT4"}, nil)
		predefinedDenied(s, "INSERT INTO pdef_rls.private_t VALUES(3)", `new row violates row-level security policy for table "private_t"`, false)
		s.actor("")
		s.exec("REVOKE pg_read_all_data,pg_write_all_data FROM rls_other")
		s.actor("rls_other")
		predefinedFailure(s, "SELECT id FROM pdef_rls.private_t", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_rls", Position: "16"}, true, "prepare")
	})
}
