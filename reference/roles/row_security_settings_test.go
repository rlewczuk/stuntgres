package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRowSecurityValues(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		rowSecurityValue(s, "on")
		for _, tc := range []struct{ input, value string }{
			{"off", "off"}, {"on", "on"}, {"true", "on"}, {"false", "off"},
			{"yes", "on"}, {"no", "off"}, {"1", "on"}, {"0", "off"},
			{"Tr", "on"}, {"Fa", "off"}, {"Y", "on"}, {"N", "off"}, {"OF", "off"},
		} {
			rowSecurityCase(s, "alias="+tc.input, func() {
				s.exec("SET row_security='" + tc.input + "'")
				rowSecurityValue(s, tc.value)
			})
		}
		for _, input := range []string{"bogus", "o", " true "} {
			rowSecurityCase(s, "invalid="+input, func() {
				s.exec("SET row_security=off")
				predefinedFailure(s, "SET row_security='"+input+"'", fixture.Diagnostic{Code: "22023", Message: `parameter "row_security" requires a Boolean value`}, false, "execute")
				rowSecurityValue(s, "off")
			})
		}
		for _, q := range []string{"SET row_security=DEFAULT", "RESET row_security", "RESET ALL"} {
			s.exec("SET row_security=off")
			s.exec(q)
			rowSecurityValue(s, "on")
		}
		for _, value := range []string{"off", "on"} {
			predefinedSelect(s, "SELECT set_config('row_security','"+value+"',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{value}})
			rowSecurityValue(s, value)
		}
		predefinedDenied(s, "ALTER TABLE t DISABLE ROW LEVEL SECURITY", "must be owner of table t", false)
		rlsBaseEnabled(s, true)
		rlsBaseRows(s, nil)
		// Protocol Prepare is quiet even for invalid values; validation belongs
		// to Execute, and no successful Prepare may install a setting.
		quiet := predefinedHeld(s, "SET row_security=off")
		rowSecurityValue(s, "on")
		fixture.RunStep(s.t, s.p.Ctx, quiet, s.record, fixture.Exec(quiet.SQL, 0))
		rowSecurityValue(s, "off")
		invalid := predefinedHeld(s, "SET row_security='bogus'")
		step := fixture.ImplicitError(invalid.SQL, fixture.Diagnostic{Code: "22023", Message: `parameter "row_security" requires a Boolean value`})
		step.ErrorStage = "execute"
		fixture.RunStep(s.t, s.p.Ctx, invalid, s.record, step)
		rowSecurityValue(s, "off")
		rowSecurityUnchanged(s)
	})
}

func TestReferenceRowSecurityEffects(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "rls_owner", func() { s.exec("CREATE TABLE rls_setting_src(id INT,v INT)") })
		s.run(fixture.Exec("INSERT INTO rls_setting_src VALUES(1,10)", 1))
		s.exec("GRANT SELECT ON rls_setting_src TO rls_reader")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		rlsBaseRows(s, nil)
		predefinedSelect(s, "SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
		rlsBaseProposalDenied(s, "INSERT INTO t VALUES(3,30),(4,40)")
		for _, q := range []string{"UPDATE t SET v=99", "DELETE FROM t", "INSERT INTO t SELECT id,v FROM rls_setting_src WHERE false", "MERGE INTO t USING rls_setting_src s ON t.id=s.id WHEN NOT MATCHED AND false THEN INSERT VALUES(s.id,s.v)"} {
			s.run(fixture.Exec(q, 0))
		}
		s.exec("SET row_security=off")
		for _, q := range []string{"SELECT id FROM t ORDER BY id", "SELECT count(*) FROM t", "SELECT id FROM t WHERE false"} {
			rowSecurityOff(s, q, "t", true, false)
		}
		for _, q := range []string{"INSERT INTO t VALUES(3,30),(4,40)", "UPDATE t SET v=99", "DELETE FROM t", "INSERT INTO t SELECT id,v FROM rls_setting_src WHERE false", "MERGE INTO t USING rls_setting_src s ON t.id=s.id WHEN NOT MATCHED AND false THEN INSERT VALUES(s.id,s.v)"} {
			rowSecurityOff(s, q, "t", false, false)
		}
		rowSecurityUnchanged(s)
		s.actor("rls_owner")
		for _, value := range []string{"on", "off"} {
			s.exec("SET row_security=" + value)
			rlsBaseRows(s, rlsBaseSeedRows())
		}
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		rowSecurityOff(s, "SELECT id FROM t", "t", true, true)
		s.exec("SET row_security=on")
		rlsBaseRows(s, nil)
		for _, actor := range []string{"rls_bypass", ""} {
			s.actor(actor)
			for _, value := range []string{"on", "off"} {
				s.exec("SET row_security=" + value)
				rlsBaseRows(s, rlsBaseSeedRows())
			}
		}
		s.actor("rls_owner")
		s.exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		for _, value := range []string{"on", "off"} {
			s.exec("SET row_security=" + value)
			rlsBaseRows(s, rlsBaseSeedRows())
		}
		// Only the source is protected: an empty input must still be analyzed.
		s.actor("rls_owner")
		s.exec("ALTER TABLE rls_setting_src ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		for _, q := range []string{
			"INSERT INTO t SELECT id,v FROM rls_setting_src WHERE false",
			"UPDATE t SET v=rls_setting_src.v FROM rls_setting_src WHERE t.id=rls_setting_src.id",
			"DELETE FROM t USING rls_setting_src WHERE t.id=rls_setting_src.id",
			"MERGE INTO t USING rls_setting_src s ON t.id=s.id WHEN NOT MATCHED AND false THEN INSERT VALUES(s.id,s.v)",
		} {
			rowSecurityOff(s, q, "rls_setting_src", false, false)
		}
		s.actor("rls_owner")
		s.exec("ALTER TABLE rls_setting_src DISABLE ROW LEVEL SECURITY")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY, NO FORCE ROW LEVEL SECURITY")
		for _, policy := range []string{
			"CREATE POLICY setting_policy ON t TO rls_other USING(TRUE)",
			"CREATE POLICY setting_policy ON t FOR DELETE USING(TRUE)",
			"CREATE POLICY setting_policy ON t AS RESTRICTIVE USING(TRUE)",
			"CREATE POLICY setting_policy ON t",
		} {
			s.exec(policy)
			s.actor("rls_reader")
			s.exec("SET row_security=on")
			rlsBaseRows(s, nil)
			s.exec("SET row_security=off")
			rowSecurityOff(s, "SELECT id FROM t", "t", true, false)
			s.actor("rls_owner")
			s.exec("DROP POLICY setting_policy ON t")
		}
		for _, tc := range []struct {
			expression string
			rows       [][]any
		}{{"TRUE", rlsBaseSeedRows()}, {"FALSE", nil}, {"NULL", nil}} {
			s.exec("CREATE POLICY setting_expression ON t FOR SELECT USING(" + tc.expression + ")")
			s.actor("rls_reader")
			s.exec("SET row_security=on")
			rlsBaseExpression(s, "SELECT id FROM t ORDER BY id", tc.rows)
			s.exec("SET row_security=off")
			rowSecurityOff(s, "SELECT id FROM t ORDER BY id", "t", true, false)
			s.actor("rls_owner")
			s.exec("DROP POLICY setting_expression ON t")
		}
		rowSecurityUnchanged(s)
	})
}
