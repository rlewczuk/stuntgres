package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Each equality evaluates its left conversion before its right WHEN operand.
// The CASE source is saved once, but conversion is performed once per reached
// equality (not once globally). These are independent PG executor expectations;
// the root runs the pinned oracle before accepting the effect/error candidates.
func TestReferenceCastDefinitionCaseBinding(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			for _, q := range []string{
				castDefinitionSource,
				"CREATE SEQUENCE case_order",
				"CREATE TABLE case_effects(n bigint, label text)",
				`CREATE FUNCTION case_source(cast_src) RETURNS cast_src LANGUAGE SQL AS $$ INSERT INTO case_effects VALUES (nextval('case_order'),'source'); SELECT $1 $$`,
				`CREATE FUNCTION case_convert(cast_src) RETURNS integer LANGUAGE SQL AS $$ INSERT INTO case_effects VALUES (nextval('case_order'),'cast'); SELECT CASE WHEN $1 IS NULL THEN 99 WHEN $1::text='red' THEN 41 ELSE 42 END $$`,
				`CREATE FUNCTION case_when(integer) RETURNS integer LANGUAGE SQL AS $$ INSERT INTO case_effects VALUES (nextval('case_order'),'when'); SELECT $1 $$`,
				"CREATE CAST (cast_src AS integer) WITH FUNCTION case_convert(cast_src) AS IMPLICIT",
			} {
				run(fixture.Exec(q, 0))
			}
			for _, prepared := range []bool{false, true} {
				run(fixture.Exec("DELETE FROM case_effects", 0))
				s := fixture.Select("SELECT CASE case_source('red'::cast_src) WHEN case_when(0) THEN 0 WHEN case_when(41) THEN 7 WHEN case_when(41) THEN 8 ELSE 9 END AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT label FROM case_effects ORDER BY n", []string{"label"}, []string{"TEXT"}, [][]any{{"source"}, {"cast"}, {"when"}, {"cast"}, {"when"}}))
				run(fixture.Exec("DELETE FROM case_effects", 5))
				s = fixture.Select("SELECT CASE case_source(NULL::cast_src) WHEN case_when(99) THEN 11 ELSE 12 END AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}})
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT label FROM case_effects ORDER BY n", []string{"label"}, []string{"TEXT"}, [][]any{{"source"}, {"cast"}, {"when"}}))
				run(fixture.Exec("DELETE FROM case_effects", 3))
				// The nested CASE has its own saved NULL; it must restore the
				// outer saved red before the second outer equality is reached.
				s = fixture.Select("SELECT CASE case_source('red'::cast_src) WHEN (CASE NULL::cast_src WHEN 99 THEN 0 ELSE -1 END) THEN 0 WHEN 41 THEN 13 ELSE 14 END AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(13)}})
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT label FROM case_effects ORDER BY n", []string{"label"}, []string{"TEXT"}, [][]any{{"source"}, {"cast"}, {"cast"}, {"cast"}}))
				run(fixture.Exec("DELETE FROM case_effects", 4))
			}
			// IO is also a selected expression recipe, not a fixed value plan.
			run(fixture.Exec("DROP CAST (cast_src AS integer)", 0))
			run(fixture.Exec("CREATE CAST (cast_src AS text) WITH INOUT AS IMPLICIT", 0))
			run(fixture.Exec("CREATE TABLE case_values(s cast_src)", 0))
			run(fixture.Exec("INSERT INTO case_values VALUES ('red'),('blue')", 2))
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT CASE 'red'::cast_src WHEN 'red'::text THEN 17 ELSE 18 END AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(17)}})
				s.Prepared = prepared
				run(s)
				s = fixture.Select("SELECT CASE s WHEN 'red'::text THEN 17 ELSE 18 END AS v, count(*) AS n FROM case_values GROUP BY CASE s WHEN 'red'::text THEN 17 ELSE 18 END ORDER BY v", []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(17), int64(1)}, {int64(18), int64(1)}})
				s.Prepared = prepared
				run(s)
			}
		})
	}
}

func TestReferenceCastDefinitionCaseErrorOrder(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			for _, q := range []string{
				castDefinitionSource,
				"CREATE SEQUENCE case_rhs_calls",
				`CREATE FUNCTION case_fail(cast_src) RETURNS integer LANGUAGE SQL AS $$ SELECT 1/(length($1::text)-3) $$`,
				`CREATE FUNCTION case_rhs() RETURNS integer LANGUAGE SQL AS $$ SELECT nextval('case_rhs_calls'); SELECT 41 $$`,
				"CREATE CAST (cast_src AS integer) WITH FUNCTION case_fail(cast_src) AS IMPLICIT",
			} {
				run(fixture.Exec(q, 0))
			}
			for _, prepared := range []bool{false, true} {
				d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				d.Context = `SQL function "case_fail" statement 1`
				// The oracle reports no routine frame for this scalar body;
				// native retains its ordinary SQL routine execution frame.
				if backend.PG {
					d.Context = ""
				}
				s := fixture.ErrorDiagnostic("SELECT CASE 'red'::cast_src WHEN case_rhs() THEN 1 ELSE 0 END", d)
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				// Sequences are not rolled back: a mistakenly evaluated RHS
				// remains observable even though the statement failed.
				run(fixture.Select("SELECT is_called FROM case_rhs_calls", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
			}
		})
	}
}
