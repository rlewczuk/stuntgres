package reference_test

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Expectations come from REL_18_6 and the pinned18.6 oracle, not native
// metadata or backend parity.
func comparisonCheckpoint(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

// Unlike fixture.Prepared alone, this exercises a genuinely retained statement
// twice, with all result cursors closed before closing the statement.
func comparisonCheckpointRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	stmt, err := b.DB.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatalf("held Prepare %s: %v", s.Query, err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: s.Query}
	for range 2 {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}

func comparisonCheckpointBool(t *testing.T, p *fixture.Pair, b fixture.Backend, expr string, want any, args ...any) {
	t.Helper()
	comparisonCheckpointRoutes(t, p, b, fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{want}}, args...))
}

func comparisonCheckpointAnalysis(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, d fixture.Diagnostic, args ...any) {
	t.Helper()
	comparisonCheckpointAnalysisRoute(t, p, b, q, d, false, args...)
	comparisonCheckpointAnalysisRoute(t, p, b, q, d, true, args...)
}

// One analysis route: direct execution fails at execute stage; Prepare fails
// genuinely at prepare stage.
func comparisonCheckpointAnalysisRoute(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, d fixture.Diagnostic, prepared bool, args ...any) {
	t.Helper()
	s := fixture.ImplicitError(q, d, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	if prepared {
		s.Prepared, s.ErrorStage = true, "prepare"
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
}

func comparisonCheckpointPosition(q, marker string) string {
	i := strings.Index(q, marker)
	if i < 0 {
		panic("comparison diagnostic marker absent")
	}
	return strconv.Itoa(utf8.RuneCountInString(q[:i]) + 1)
}

// Small new-gap witnesses, independent of the already reported NAME/VARCHAR
// failure. Source: REL_18_6 pg_cast.dat and pg_operator.dat; parse_oper.c.
func TestReferenceComparisonCoreConversions(t *testing.T) {
	for _, tc := range []struct {
		name, expr string
		want       any
	}{
		{"time_interval", "TIME '01:00' = INTERVAL '1 hour'", true},
		{"interval_time", "INTERVAL '1 hour' >= TIME '01:00'", true},
		{"internal_char_text", `(SELECT relkind FROM pg_class WHERE oid='pg_class'::regclass) < 'z'::text`, true},
		{"text_internal_char", `'z'::text > (SELECT relkind FROM pg_class WHERE oid='pg_class'::regclass)`, true},
		{"oid_int4", "'2147483648'::oid > 2147483647::int4", true},
		{"int4_oid", "2147483647::int4 < '2147483648'::oid", true},
		{"aliases", "'23'::regtype = '23'::regclass", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
				comparisonCheckpointBool(t, p, b, tc.expr, tc.want)
			})
		})
	}
}

// Unknowns are a separate dimension, not both-typed-NULL admission evidence.
// Domain truth coercion is specified by parse_expr.c:transformBooleanTest and
// parse_coerce.c:coerce_to_boolean, not descriptor pointer equality.
func TestReferenceComparisonUnknownParameters(t *testing.T) {
	t.Run("boolean_domain", func(t *testing.T) {
		comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN comparison_bool AS boolean", 0))
			forms := []string{"IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN"}
			for i, value := range []string{"TRUE", "FALSE", "NULL"} {
				want := [3][6]bool{{true, false, false, true, false, true}, {false, true, true, false, false, true}, {false, true, false, true, true, false}}
				for j, form := range forms {
					t.Run(value+"_"+form, func(t *testing.T) {
						comparisonCheckpointBool(t, p, b, "("+value+"::comparison_bool) "+form, want[i][j])
					})
				}
			}
			q := "SELECT ($1::comparison_bool) IS TRUE AS v"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			for i, arg := range []any{true, false, nil, true} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{i == 0 || i == 3}}, arg))
			}
		})
	})
	t.Run("unresolved_nulltest", func(t *testing.T) {
		comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			for _, form := range []string{"IS NULL", "IS NOT NULL", "ISNULL", "NOTNULL"} {
				q := "/*é*/ SELECT $1 " + form
				comparisonCheckpointAnalysis(t, p, b, q, fixture.Diagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"}, nil)
			}
		})
	})
	t.Run("unknown_exact_and_typed_null", func(t *testing.T) {
		comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			for _, tc := range []struct {
				expr string
				want any
			}{
				{"'a' = 'a'", true}, {"NULL = NULL", nil}, {"'1' = 1::int2", true},
				{"1::int8 = '1'", true}, {"NULL = 1::int4", nil}, {"1::int4 = NULL", nil},
				{"'0'::cid IS DISTINCT FROM NULL::cid", true}, {"NULL::cid IS NOT DISTINCT FROM NULL::cid", true},
			} {
				comparisonCheckpointBool(t, p, b, tc.expr, tc.want)
			}
		})
	})
	t.Run("nonboolean_even_null", func(t *testing.T) {
		comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			for _, form := range []string{"IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN"} {
				q := "/*é*/ SELECT NULL::integer " + form
				comparisonCheckpointAnalysis(t, p, b, q, fixture.Diagnostic{Code: "42804", Message: "argument of " + form + " must be type boolean, not type integer", Position: comparisonCheckpointPosition(q, "NULL")})
			}
		})
	})
}
