package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func rlsCheckCases(t *testing.T, body func(*membershipScenario)) {
	t.Helper()
	membershipCases(t, []string{"check_writer"}, func(s *membershipScenario) {
		s.exec("GRANT USAGE ON SCHEMA public TO check_writer")
		defer s.c.ExecContext(s.p.Ctx, "RESET ROLE")
		body(s)
	})
}

func rlsCheckEnable(s *membershipScenario) {
	s.t.Helper()
	s.exec("GRANT SELECT,INSERT,UPDATE ON t TO check_writer")
	s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
}

func rlsCheckPolicies(s *membershipScenario, check string) {
	s.t.Helper()
	rlsCheckEnable(s)
	s.exec("CREATE POLICY sel ON t FOR SELECT USING(TRUE)")
	s.exec("CREATE POLICY ins ON t FOR INSERT WITH CHECK(" + check + ")")
	s.exec("CREATE POLICY upd ON t FOR UPDATE USING(TRUE) WITH CHECK(" + check + ")")
}

func rlsCheckRows(s *membershipScenario, rows [][]any) {
	s.t.Helper()
	s.run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, rows))
}

func rlsCheckGeneratedRows(s *membershipScenario, rows [][]any) {
	s.t.Helper()
	s.run(fixture.Select("SELECT id,v,g FROM t ORDER BY id", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, rows))
}

func rlsCheckTrigger(s *membershipScenario, table, delta string) {
	s.t.Helper()
	s.exec("CREATE FUNCTION check_transform() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.id=99 THEN RETURN NULL; END IF; NEW.v := NEW.v+(" + delta + "); RETURN NEW; END$$")
	s.exec("CREATE TRIGGER transform BEFORE INSERT OR UPDATE ON " + table + " FOR EACH ROW EXECUTE FUNCTION check_transform()")
}

func rlsCheckAborted(s *membershipScenario) {
	s.t.Helper()
	// Direct Query remains usable as an assertion even if preparing a new
	// statement in the failed transaction would itself raise 25P02.
	step := fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
	step.SelectRows = true
	fixture.RunStep(s.t, s.p.Ctx, s.c, s.record, step)
}

// Query can fail immediately or while consuming RETURNING on a streaming
// backend. In either case close the cursor before transaction recovery.
func rlsCheckDeny(s *membershipScenario, q string, query bool) {
	s.t.Helper()
	if !query {
		combinationDeny(s, q, "", false)
		return
	}
	var rows *sql.Rows
	var err error
	if s.prepared {
		stmt, prepareErr := s.c.PrepareContext(s.p.Ctx, q)
		if prepareErr != nil {
			s.t.Fatal(prepareErr)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else {
		rows, err = s.c.QueryContext(s.p.Ctx, q)
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
	fixture.AssertDiagnostic(s.t, err, &want)
}
