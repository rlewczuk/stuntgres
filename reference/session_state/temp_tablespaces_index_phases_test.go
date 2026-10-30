package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func tempTablespacesMissingMethod() fixture.Diagnostic {
	return physicalDiagnostic("42704", `access method "tt_missing_method" does not exist`)
}

func tempTablespacesPhaseSeed(s *physicalCase) {
	s.t.Helper()
	// An INT-only table initializes the TEMP namespace before the measured
	// transaction and carries the exact logical data that errors must preserve.
	s.run(fixture.Exec("CREATE TEMP TABLE tt_sentinel(v INT)", 0))
	s.run(fixture.Exec("INSERT INTO tt_sentinel VALUES(73)", 1))
}

// The first valid creation calibrates only the unspecified initial phase. Every
// subsequent expected outcome is fixed by circular selection, not parity/random
// distribution. Preparation itself must neither fail nor consume a destination.
func tempTablespacesPhaseCalibrate(s *physicalCase, name string, prepared bool) bool {
	s.t.Helper()
	s.run(fixture.Exec("SAVEPOINT tt_phase_attempt", 0))
	query := "CREATE TEMP TABLE "+name+"(v INT)"
	var result sql.Result
	var err error
	if prepared {
		stmt := s.tempTablespacesHeld(query)
		defer stmt.Close()
		result, err = stmt.ExecContext(s.p.Ctx)
	} else {
		result, err = s.conn.ExecContext(s.p.Ctx, query)
	}
	success := err == nil
	if success {
		count, err := result.RowsAffected()
		if err != nil || count != 0 { s.t.Fatalf("calibration affected=%d: %v", count, err) }
	} else {
		d := defaultTablespaceGlobal()
		fixture.AssertDiagnostic(s.t, err, &d)
		s.run(fixture.Exec("ROLLBACK TO tt_phase_attempt", 0))
	}
	s.run(fixture.Exec("RELEASE tt_phase_attempt", 0))
	count := int64(0)
	if success { count = 1 }
	s.run(fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace=pg_my_temp_schema() AND relname="+physicalLiteral(name), []string{"count"}, []string{"INT8"}, [][]any{{count}}))
	if !success { s.tempTablespacesAbsent(name) }
	return success
}

func tempTablespacesPhaseExpected(s *physicalCase, name string, prepared, success bool) {
	s.t.Helper()
	s.run(fixture.Exec("SAVEPOINT tt_phase_attempt", 0))
	query := "CREATE TEMP TABLE "+name+"(v INT)"
	step := fixture.Exec(query, 0)
	if !success { step = physicalError(query, defaultTablespaceGlobal(), false, false) }
	step.Prepared = prepared
	s.run(step)
	if !success { s.run(fixture.Exec("ROLLBACK TO tt_phase_attempt", 0)) }
	s.run(fixture.Exec("RELEASE tt_phase_attempt", 0))
	count := int64(0)
	if success { count = 1 }
	s.run(fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace=pg_my_temp_schema() AND relname="+physicalLiteral(name), []string{"count"}, []string{"INT8"}, [][]any{{count}}))
	if !success { s.tempTablespacesAbsent(name) }
}

func tempTablespacesIndexDefinitionPhases(t *testing.T, modes []bool) {
	for _, prepared := range modes {
		t.Run(fmt.Sprintf("prepared-%t", prepared), func(t *testing.T) {
			t.Run("method-competition", func(t *testing.T) {
				physicalCases(t, nil, func(s *physicalCase) {
					tempTablespacesPhaseSeed(s)
					const query = "CREATE INDEX tt_phase_bad ON tt_sentinel USING tt_missing_method(v)"
					// The empty control reaches late method lookup. With global,
					// destination selection wins over that same invalid method.
					s.run(physicalError(query, tempTablespacesMissingMethod(), prepared, false))
					s.tempTablespacesAbsent("tt_phase_bad")
					s.run(fixture.Exec("SET temp_tablespaces='pg_global'", 0))
					s.run(physicalError(query, defaultTablespaceGlobal(), prepared, false))
					s.tempTablespacesAbsent("tt_phase_bad")
					s.tablespaceAbsent("pg_indexes", "indexname", "tt_phase_bad")
					s.tempTablespacesReads("pg_global", "session")
					s.tempTablespacesSentinel()
				})
			})
			t.Run("late-error-consumption", func(t *testing.T) {
				physicalCases(t, nil, func(s *physicalCase) {
					tempTablespacesPhaseSeed(s)
					const query = "CREATE INDEX tt_phase_bad ON tt_sentinel USING tt_missing_method(v)"
					var held *sql.Stmt
					if prepared {
						held = s.tempTablespacesHeld(query)
						defer held.Close()
					}
					s.run(fixture.Exec("BEGIN", 0))
					s.run(clientText("SELECT set_config('temp_tablespaces','pg_default, pg_global',false)", "set_config", "pg_default, pg_global"))
					firstDefault := false
					for i := range 2 {
						s.run(fixture.Exec("SAVEPOINT tt_phase_attempt", 0))
						var err error
						if prepared { _, err = held.ExecContext(s.p.Ctx) } else { _, err = s.conn.ExecContext(s.p.Ctx, query) }
						if err == nil { s.t.Fatal("invalid access method unexpectedly created an index") }
						want := defaultTablespaceGlobal()
						if i == 0 {
							d := fixture.ExtractDiagnostic(s.t, err)
							if d.Code == "42704" { firstDefault = true }
							if firstDefault { want = tempTablespacesMissingMethod() }
						} else if !firstDefault {
							want = tempTablespacesMissingMethod()
						}
						fixture.AssertDiagnostic(s.t, err, &want)
						s.run(fixture.Exec("ROLLBACK TO tt_phase_attempt", 0))
						s.run(fixture.Exec("RELEASE tt_phase_attempt", 0))
					}
					// Two failed index attempts consumed a complete cycle. A valid
					// pair therefore starts at the same independently observed phase.
					tempTablespacesPhaseExpected(s, "tt_phase_after_0", prepared, firstDefault)
					tempTablespacesPhaseExpected(s, "tt_phase_after_1", prepared, !firstDefault)
					s.tempTablespacesAbsent("tt_phase_bad")
					s.tablespaceAbsent("pg_indexes", "indexname", "tt_phase_bad")
					s.tempTablespacesSentinel()
					s.run(fixture.Exec("COMMIT", 0))
					s.tempTablespacesReads("pg_default, pg_global", "session")
				})
			})
			for _, tc := range []struct { name, query string }{
				{"expression", "CREATE INDEX tt_phase_early ON tt_sentinel ((tt_missing_column + 1))"},
				{"predicate", "CREATE INDEX tt_phase_early ON tt_sentinel(v) WHERE tt_missing_column > 0"},
			} {
				t.Run("early-transform-"+tc.name, func(t *testing.T) {
					physicalCases(t, nil, func(s *physicalCase) {
						tempTablespacesPhaseSeed(s)
						s.run(fixture.Exec("BEGIN", 0))
						s.run(clientText("SELECT set_config('temp_tablespaces','pg_default, pg_global',false)", "set_config", "pg_default, pg_global"))
						firstDefault := tempTablespacesPhaseCalibrate(s, "tt_phase_before", prepared)
						s.run(fixture.Exec("SAVEPOINT tt_phase_early", 0))
						d := physicalDiagnostic("42703", `column "tt_missing_column" does not exist`)
						d.Position = fixture.Position(strings.Index(tc.query, "tt_missing_column")+1)
						s.run(physicalError(tc.query, d, prepared, false))
						s.run(fixture.Exec("ROLLBACK TO tt_phase_early", 0))
						s.run(fixture.Exec("RELEASE tt_phase_early", 0))
						// Transformation failed before selection: only calibration
						// consumed, so the next valid relation must use its opposite.
						tempTablespacesPhaseExpected(s, "tt_phase_after_early", prepared, !firstDefault)
						s.tempTablespacesAbsent("tt_phase_early")
						s.tablespaceAbsent("pg_indexes", "indexname", "tt_phase_early")
						s.tempTablespacesSentinel()
						s.run(fixture.Exec("COMMIT", 0))
					})
				})
			}
		})
	}
}

func TestReferenceTempTablespacesIndexDefinitionPhases(t *testing.T) {
	tempTablespacesIndexDefinitionPhases(t, []bool{false, true})
}
