package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeComparisonDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, q, code, message, hint, marker string
		rows                                 bool
	}{
		{"absent_type", "SELECT '1'::cube = '1'::cube", "42704", `type "cube" does not exist`, "", "cube", true},
		{"absent_function", "SELECT cube_eq(1,1)", "42883", "function cube_eq(integer, integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "cube_eq", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cubeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Hint, want.Position = tc.hint, strconv.Itoa(strings.Index(tc.q, tc.marker)+1)
				for _, prepared := range []bool{false, true} {
					cubeError(t, p, b, c, tc.q, prepared, tc.rows, true, want)
				}
			})
		})
	}
	for _, tc := range []struct {
		name, q, code, message, hint, marker string
		rows, prepareFailure                 bool
	}{
		{"cube_text_operator", "SELECT '1'::cube = '1'::text", "42883", "operator does not exist: cube = text", "No operator matches the given name and argument types. You might need to add explicit type casts.", "=", true, true},
		{"cube_integer_operator", "SELECT '1'::cube < 1", "42883", "operator does not exist: cube < integer", "No operator matches the given name and argument types. You might need to add explicit type casts.", "<", true, true},
		{"cube_text_function", "SELECT cube_eq('1'::cube,'1'::text)", "42883", "function cube_eq(cube, text) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "cube_eq", true, true},
		{"cube_integer_function", "SELECT cube_cmp('1'::cube,1)", "42883", "function cube_cmp(cube, integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "cube_cmp", true, true},
		{"hash_default", "CREATE INDEX cube_compare_bad ON cube_compare_diag USING hash(v)", "42704", `data type cube has no default operator class for access method "hash"`, "You must specify an operator class for the index or define a default operator class for the data type.", "", false, false},
		{"wrong_btree_class", "CREATE INDEX cube_compare_bad ON cube_compare_diag(v pg_catalog.text_ops)", "42804", `operator class "pg_catalog.text_ops" does not accept data type cube`, "", "", false, false},
		{"missing_btree_class", "CREATE INDEX cube_compare_bad ON cube_compare_diag(v cube_compare_missing_ops)", "42704", `operator class "cube_compare_missing_ops" does not exist for access method "btree"`, "", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cubeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				cubeInstall(t, p, b, c)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE cube_compare_diag(id integer,v cube)", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO cube_compare_diag VALUES (1,'1')", 1))
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Hint = tc.hint
				if tc.marker != "" {
					want.Position = strconv.Itoa(strings.Index(tc.q, tc.marker) + 1)
				}
				for _, prepared := range []bool{false, true} {
					cubeError(t, p, b, c, tc.q, prepared, tc.rows, tc.prepareFailure, want)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, cubeComparisonIDs("SELECT id FROM cube_compare_diag WHERE v='1'::cube ORDER BY id", 1))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='cube_compare_bad'", []string{"relname"}, []string{"NAME"}, [][]any{}))
				}
			})
		})
	}
	for _, route := range []struct {
		name, q, marker string
		args            []any
		parameter       int
		rows            bool
	}{
		{"literal_comparison", "SELECT '(1,)'::cube = '1'::cube", "'(1,)'", nil, 0, true},
		{"literal_function", "SELECT cube_cmp('1'::cube,'(1,)'::cube)", "'(1,)'", nil, 0, true},
		{"bound_comparison", "SELECT $1::cube = $2::cube", "", []any{"1", "(1,)"}, 2, true},
		{"literal_atomic", "INSERT INTO cube_compare_diag VALUES (2,'2'),(3,'(1,)')", "'(1,)'", nil, 0, false},
		{"bound_atomic", "INSERT INTO cube_compare_diag VALUES (2,$1),(3,$2)", "", []any{"2", []byte("(1,)")}, 2, false},
	} {
		t.Run(route.name, func(t *testing.T) {
			cubeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				cubeInstall(t, p, b, c)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE cube_compare_diag(id integer,v cube)", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO cube_compare_diag VALUES (1,'1')", 1))
				want := cubeSyntaxDiagnostic(`syntax error at or near ")"`)
				if route.parameter > 0 {
					want = cubeParameterDiagnostic(want, "(1,)", route.parameter)
				} else {
					want.Position = strconv.Itoa(strings.Index(route.q, route.marker) + 1)
				}
				for _, prepared := range []bool{false, true} {
					cubeError(t, p, b, c, route.q, prepared, route.rows, route.parameter == 0, want, route.args...)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, cubeComparisonIDs("SELECT id FROM cube_compare_diag WHERE v='1'::cube ORDER BY id", 1))
				}
			})
		})
	}
	t.Run("installation_conflict_atomic", func(t *testing.T) {
		cubeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TYPE cube AS ENUM ('occupied')", 0))
			want := fixture.ControlsReferenceDiagnostic("42710", `type "cube" already exists`)
			want.Context = "SQL statement \"CREATE TYPE cube (\n\tINTERNALLENGTH = variable,\n\tINPUT = cube_in,\n\tOUTPUT = cube_out,\n\tALIGNMENT = double\n)\"\nextension script file \"cube--1.2.sql\", near line 26"
			for _, prepared := range []bool{false, true} {
				cubeError(t, p, b, c, "CREATE EXTENSION cube", prepared, false, false, want)
				run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='cube'", []string{"extname"}, []string{"NAME"}, [][]any{}))
				run(fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('cube_in','cube_out','cube_eq','cube_ne','cube_lt','cube_le','cube_gt','cube_ge','cube_cmp') ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{}))
				run(fixture.Select("SELECT opcname FROM pg_opclass WHERE opcnamespace='public'::regnamespace AND opcname='cube_ops'", []string{"opcname"}, []string{"NAME"}, [][]any{}))
				run(fixture.Select("SELECT opfname FROM pg_opfamily WHERE opfnamespace='public'::regnamespace AND opfname='cube_ops'", []string{"opfname"}, []string{"NAME"}, [][]any{}))
				fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT 'occupied'::cube AS v", []string{"v"}, []string{""}, [][]any{{"occupied"}}))
			}
		})
	})
}
