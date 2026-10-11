package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumFunctionsStableAdmission(t *testing.T) {
	enumFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		for i, expression := range []string{
			"enum_first(NULL::mood) = 'z'::mood",
			"enum_last(NULL::mood) = 'm'::mood",
			"enum_range(NULL::mood) IS NOT NULL",
			"enum_range(NULL::mood,NULL::mood) IS NOT NULL",
			"enum_first(lo) = 'z'::mood",
			"enum_last(lo) = 'm'::mood",
			"enum_range(lo) IS NOT NULL",
			"enum_range(lo,hi) IS NOT NULL",
		} {
			for _, tc := range []struct {
				query, name, message string
			}{
				{fmt.Sprintf("CREATE TABLE enum_stable_generated_%d(lo mood,hi mood,v boolean GENERATED ALWAYS AS (%s) STORED)", i, expression), fmt.Sprintf("enum_stable_generated_%d", i), "generation expression is not immutable"},
				{fmt.Sprintf("CREATE INDEX enum_stable_expression_%d ON enum_endpoints ((%s))", i, expression), fmt.Sprintf("enum_stable_expression_%d", i), "functions in index expression must be marked IMMUTABLE"},
				{fmt.Sprintf("CREATE INDEX enum_stable_predicate_%d ON enum_endpoints (id) WHERE %s", i, expression), fmt.Sprintf("enum_stable_predicate_%d", i), "functions in index predicate must be marked IMMUTABLE"},
			} {
				t.Run(tc.query, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(tc.query, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P17", Message: tc.message})
						s.Prepared = prepared
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
					}
					name := tc.name
					if strings.HasPrefix(tc.query, "CREATE TABLE") {
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE "+name+"(id integer)", 0))
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("DROP TABLE "+name, 0))
					} else {
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE INDEX "+name+" ON enum_endpoints(id)", 0))
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("DROP INDEX "+name, 0))
					}
				})
			}
		}
		// An eliminated branch is not a surviving STABLE call.
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE enum_stable_eliminated(v mood GENERATED ALWAYS AS (CASE WHEN false THEN enum_first(NULL::mood) ELSE 'a'::mood END) STORED)", 0))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("INSERT INTO enum_stable_eliminated DEFAULT VALUES", 1))
		fixture.EnumReferenceRunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT v FROM enum_stable_eliminated", []string{"v"}, []string{""}, [][]any{{"a"}}))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT id,lo::text AS lo,hi::text AS hi FROM enum_endpoints ORDER BY id", []string{"id", "lo", "hi"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "z", "a"}, {int64(2), "a", "m"}, {int64(3), nil, "a"}, {int64(4), "a", nil}, {int64(5), nil, nil}}))
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("CREATE TABLE generated_enum(v mood GENERATED ALWAYS AS (enum_first(NULL::mood)) STORED)", fixture.ControlsReferenceDiagnostic("42P17", "generation expression is not immutable"))
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE generated_enum(id integer)", 0))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE INDEX enum_stable_eliminated_expression ON enum_endpoints ((CASE WHEN false THEN enum_first(lo) = 'z'::mood ELSE true END))", 0))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE INDEX enum_stable_eliminated_predicate ON enum_endpoints(id) WHERE CASE WHEN false THEN enum_range(lo,hi) IS NOT NULL ELSE true END", 0))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT id FROM enum_endpoints WHERE CASE WHEN false THEN enum_range(lo,hi) IS NOT NULL ELSE true END ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}))
	})
}

func TestReferenceEnumFunctionsStorageBoundaries(t *testing.T) {
	enumFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		for _, materialized := range []bool{false, true} {
			for _, noData := range []bool{false, true} {
				name := fmt.Sprintf("enum_range_storage_%t_%t", materialized, noData)
				kind, suffix := "TABLE", " WITH DATA"
				if materialized {
					kind = "MATERIALIZED VIEW"
				}
				if noData {
					suffix = " WITH NO DATA"
				}
				q := "CREATE " + kind + " " + name + " AS SELECT enum_range(NULL::mood) AS v" + suffix
				t.Run(q, func(t *testing.T) {
					if !b.PG {
						// Native deliberately does not support enum-array storage;
						// this is not a PostgreSQL diagnostic expectation.
						for _, prepared := range []bool{false, true} {
							s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "unsupported array element type"))
							s.Prepared, s.ErrorStage = prepared, "execute"
							fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
						}
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE "+name+"(id integer)", 0))
						return
					}
					count := int64(1)
					if noData {
						count = 0
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, count))
					if materialized && noData {
						// PostgreSQL creates an unpopulated materialized view, not
						// an empty readable table. Refresh proves the stored type.
						for _, prepared := range []bool{false, true} {
							s := fixture.MaterializedViewUnpopulated(name)
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
						}
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("REFRESH MATERIALIZED VIEW "+name, 0))
					}
					rows := [][]any{{"{z,a,m}"}}
					if noData && !materialized {
						rows = [][]any{}
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT v::text AS v FROM "+name, []string{"v"}, []string{"TEXT"}, rows))
				})
			}
		}
		for _, tc := range []struct {
			name, typ, state string
		}{
			{"enum_array_direct", "mood[]", "0A000"},
			{"enum_array_associated", "_mood", "0A000"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := "CREATE TABLE " + tc.name + "(v " + tc.typ + ")"
				cast := "SELECT '{z,a}'::" + tc.typ + "::text AS v"
				if b.PG {
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0))
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("INSERT INTO "+tc.name+" VALUES ('{z,a}')", 1))
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT v::text AS v FROM "+tc.name, []string{"v"}, []string{"TEXT"}, [][]any{{"{z,a}"}}))
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select(cast, []string{"v"}, []string{"TEXT"}, [][]any{{"{z,a}"}}))
				} else {
					message := "unsupported array element type"
					if tc.typ == "_mood" {
						message = "unsupported column type"
					}
					for _, query := range []string{q, cast} {
						for _, prepared := range []bool{false, true} {
							want := fixture.ControlsReferenceDiagnostic(tc.state, message)
							want.Position = strconv.Itoa(strings.LastIndex(query, tc.typ) + 1)
							s := fixture.ErrorDiagnostic(query, want)
							s.Prepared, s.ErrorStage = prepared, "execute"
							if query == cast {
								s.SelectRows = true
								if prepared {
									s.ErrorStage = "prepare"
								}
							}
							fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
						}
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE "+tc.name+"(id integer)", 0))
				}
			})
		}
		for _, tc := range []struct {
			name, typ, state string
		}{
			{"enum_array_domain_direct", "mood[]", "0A000"},
			{"enum_array_domain_associated", "_mood", "42804"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := "CREATE DOMAIN " + tc.name + " AS " + tc.typ
				if b.PG {
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0))
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE "+tc.name+"_stored(v "+tc.name+")", 0))
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("INSERT INTO "+tc.name+"_stored VALUES ('{a,m}')", 1))
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT v::text AS v FROM "+tc.name+"_stored", []string{"v"}, []string{"TEXT"}, [][]any{{"{a,m}"}}))
				} else {
					message := "unsupported array element type"
					if tc.typ == "_mood" {
						message = `"_mood" is not a valid base type for a domain`
					}
					for _, prepared := range []bool{false, true} {
						want := fixture.ControlsReferenceDiagnostic(tc.state, message)
						want.Position = strconv.Itoa(strings.LastIndex(q, tc.typ) + 1)
						s := fixture.ErrorDiagnostic(q, want)
						s.Prepared, s.ErrorStage = prepared, "execute"
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE DOMAIN "+tc.name+" AS integer", 0))
				}
			})
		}
		// Computation and ordinary views remain legal, as do the existing
		// projected arrays whose elements are domains over an enum.
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE VIEW enum_range_projection AS SELECT enum_range(NULL::mood) AS v", 0))
		for _, prepared := range []bool{false, true} {
			enumFunctionsSelect(t, p, b, conn, "SELECT enum_range(NULL::mood) AS v", "{z,a,m}", true, prepared)
			view := fixture.Select("SELECT v FROM enum_range_projection", []string{"v"}, []string{enumFunctionsTypeName(b.PG, "_MOOD")}, [][]any{{"{z,a,m}"}})
			view.RawTypes = []string{"_ENUM"}
			view.Prepared = prepared
			view.CheckMetadata = enumFunctionsMetadataNullable(b.PG, true, true)
			fixture.EnumReferenceRunStep(t, p.Ctx, conn, b.Recorder, view)
			s := fixture.Select("SELECT '{z,a}'::mood_domain[]::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"{z,a}"}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE enum_nonenum_array_storage AS SELECT '{1,2}'::integer[] AS v", 1))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE MATERIALIZED VIEW enum_nonenum_array_materialized AS SELECT '{1,2}'::integer[] AS v WITH DATA", 1))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT v::text AS v FROM enum_nonenum_array_materialized", []string{"v"}, []string{"TEXT"}, [][]any{{"{1,2}"}}))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE enum_scalar_storage AS SELECT enum_first(NULL::mood) AS v", 1))
		fixture.EnumReferenceRunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT v FROM enum_scalar_storage", []string{"v"}, []string{""}, [][]any{{"z"}}))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT id,lo::text AS lo,hi::text AS hi FROM enum_endpoints ORDER BY id", []string{"id", "lo", "hi"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "z", "a"}, {int64(2), "a", "m"}, {int64(3), nil, "a"}, {int64(4), "a", nil}, {int64(5), nil, nil}}))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT v::text AS v FROM enum_nonenum_array_storage", []string{"v"}, []string{"TEXT"}, [][]any{{"{1,2}"}}))
	})
}
