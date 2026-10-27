package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func rlsCheckCases(t *testing.T, body func(*fixture.RoleMembershipScenario)) {
	t.Helper()
	fixture.RoleMembershipCases(t, []string{"check_writer"}, func(s *fixture.RoleMembershipScenario) {
		s.Exec("GRANT USAGE ON SCHEMA public TO check_writer")
		defer s.C.ExecContext(s.P.Ctx, "RESET ROLE")
		body(s)
	})
}

func rlsCheckEnable(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	s.Exec("GRANT SELECT,INSERT,UPDATE ON t TO check_writer")
	s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
}

func rlsCheckPolicies(s *fixture.RoleMembershipScenario, check string) {
	s.T.Helper()
	rlsCheckEnable(s)
	s.Exec("CREATE POLICY sel ON t FOR SELECT USING(TRUE)")
	s.Exec("CREATE POLICY ins ON t FOR INSERT WITH CHECK(" + check + ")")
	s.Exec("CREATE POLICY upd ON t FOR UPDATE USING(TRUE) WITH CHECK(" + check + ")")
}

func rlsCheckRows(s *fixture.RoleMembershipScenario, rows [][]any) {
	s.T.Helper()
	s.Run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, rows))
}

func rlsCheckGeneratedRows(s *fixture.RoleMembershipScenario, rows [][]any) {
	s.T.Helper()
	s.Run(fixture.Select("SELECT id,v,g FROM t ORDER BY id", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, rows))
}

func rlsCheckTrigger(s *fixture.RoleMembershipScenario, table, delta string) {
	s.T.Helper()
	s.Exec("CREATE FUNCTION check_transform() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.id=99 THEN RETURN NULL; END IF; NEW.v := NEW.v+(" + delta + "); RETURN NEW; END$$")
	s.Exec("CREATE TRIGGER transform BEFORE INSERT OR UPDATE ON " + table + " FOR EACH ROW EXECUTE FUNCTION check_transform()")
}

func rlsCheckAborted(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	// Direct Query remains usable as an assertion even if preparing a new
	// statement in the failed transaction would itself raise 25P02.
	step := fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
	step.SelectRows = true
	fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
}

// Query can fail immediately or while consuming RETURNING on a streaming
// backend. In either case close the cursor before transaction recovery.
func rlsCheckDeny(s *fixture.RoleMembershipScenario, q string, query bool) {
	s.T.Helper()
	if !query {
		combinationDeny(s, q, "", false)
		return
	}
	var rows *sql.Rows
	var err error
	if s.Prepared {
		stmt, prepareErr := s.C.PrepareContext(s.P.Ctx, q)
		if prepareErr != nil {
			s.T.Fatal(prepareErr)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(s.P.Ctx)
	} else {
		rows, err = s.C.QueryContext(s.P.Ctx, q)
	}
	if rows != nil {
		for rows.Next() {
			// Consume the complete command, not merely its allowed prefix.
		}
		if err == nil {
			err = rows.Err()
		}
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
	}
	want := fixture.ControlsReferenceDiagnostic("42501", `new row violates row-level security policy for table "t"`)
	fixture.AssertDiagnostic(s.T, err, &want)
}
