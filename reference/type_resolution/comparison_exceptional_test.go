package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var comparisonTruthForms = []string{"IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN"}

func TestReferenceComparisonAnyarrayCatalog(t *testing.T) {
	for _, route := range []string{"symbols", "distinct", "between", "truth", "null", "conditional"} {
		t.Run(route, func(t *testing.T) {
			comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE comparison_anyarray_source(v integer)", 0))
				x := "(SELECT attmissingval FROM pg_attribute WHERE attrelid='comparison_anyarray_source'::regclass AND attnum=1)"
				// REL_18_6 parse_coerce.c:2186-2206,2375-2389 counts both
				// ANYARRAY formals, including an unknown actual. make_op calls
				// enforcement without an error-position callback: no hint/position.
				generic := fixture.Diagnostic{Code: "42804", Message: `cannot determine element type of "anyarray" argument`}
				// Mixed known identities are rejected earlier: can_coerce_type's
				// check_generic_type_consistency filters the ANYARRAY candidate
				// (parse_coerce.c:1782-1789), so make_op never enforces it.
				noMatch := func(q, left, right, op, marker string) {
					canonical := op
					if canonical == "!=" {
						canonical = "<>"
					}
					comparisonCheckpointAnalysis(t, p, b, q, fixture.Diagnostic{
						Code: "42883", Message: "operator does not exist: " + left + " " + canonical + " " + right,
						Hint:     "No operator matches the given name and argument types. You might need to add explicit type casts.",
						Position: comparisonCheckpointPosition(q, marker),
					})
				}
				switch route {
				case "symbols":
					for _, op := range comparisonSpellings {
						for _, pair := range [][2]string{{x, x}, {x, "NULL"}, {"NULL", x}} {
							comparisonCheckpointAnalysis(t, p, b, "SELECT "+pair[0]+" "+op+" "+pair[1]+" AS v", generic)
						}
						for _, concrete := range []string{"ARRAY[1]", "NULL::integer[]"} {
							q := "SELECT " + x + " " + op + " " + concrete + " AS v"
							noMatch(q, "anyarray", "integer[]", op, op+" "+concrete)
							q = "SELECT " + concrete + " " + op + " " + x + " AS v"
							noMatch(q, "integer[]", "anyarray", op, op+" "+x)
						}
					}
				case "distinct":
					for i, form := range []string{"IS DISTINCT FROM", "IS NOT DISTINCT FROM"} {
						comparisonCheckpointAnalysis(t, p, b, "SELECT "+x+" "+form+" "+x+" AS v", generic)
						q := "SELECT " + x + " " + form + " NULL::integer[] AS v"
						noMatch(q, "anyarray", "integer[]", "=", form)
						q = "SELECT NULL::integer[] " + form + " " + x + " AS v"
						noMatch(q, "integer[]", "anyarray", "=", form)
						// transformAExprDistinct recognizes raw NULL before make_op;
						// this is whole-value nullness, not generic operator admission.
						comparisonCheckpointBool(t, p, b, x+" "+form+" NULL", i == 1)
						comparisonCheckpointBool(t, p, b, "NULL "+form+" "+x, i == 1)
					}
				case "between":
					for i, form := range comparisonBetweenForms {
						first, second := ">=", "<="
						if i%2 == 1 {
							first, second = "<", ">"
						}
						for _, bounds := range [][2]string{{x, x}, {x, "NULL"}, {"NULL", x}, {"NULL", "NULL"}} {
							comparisonCheckpointAnalysis(t, p, b, "SELECT "+x+" "+form+" "+bounds[0]+" AND "+bounds[1]+" AS v", generic)
						}
						q := "SELECT " + x + " " + form + " NULL::integer[] AND NULL::integer[] AS v"
						noMatch(q, "anyarray", "integer[]", first, form)
						for _, concrete := range []string{"ARRAY[1]", "NULL::integer[]"} {
							// The first comparison may be concrete/concrete, but
							// the second is still analyzed before strict execution.
							q := "SELECT " + concrete + " " + form + " " + concrete + " AND " + x + " AS v"
							noMatch(q, "integer[]", "anyarray", second, form)
							q = "SELECT " + concrete + " " + form + " " + x + " AND " + concrete + " AS v"
							noMatch(q, "integer[]", "anyarray", first, form)
						}
					}
				case "truth":
					for _, form := range comparisonTruthForms {
						q := "SELECT " + x + " " + form + " AS v"
						comparisonCheckpointAnalysis(t, p, b, q, fixture.Diagnostic{Code: "42804", Message: "argument of " + form + " must be type boolean, not type anyarray", Position: "8"})
					}
				case "null":
					for i, form := range []string{"IS NULL", "IS NOT NULL", "ISNULL", "NOTNULL"} {
						comparisonCheckpointBool(t, p, b, x+" "+form, i%2 == 0)
					}
				case "conditional":
					source := " FROM pg_attribute WHERE attrelid='comparison_anyarray_source'::regclass AND attnum=1"
					for _, expression := range []string{
						"NULLIF(attmissingval,attmissingval)",
						"CASE attmissingval WHEN attmissingval THEN TRUE ELSE FALSE END",
					} {
						comparisonCheckpointAnalysis(t, p, b, "SELECT "+expression+source, generic)
					}
				}
			})
		})
	}
}

// The existing intarray extension/type input route is used unchanged. No
// extension operation or stock finite-universe identity is added here.
func TestReferenceComparisonInstalledQueryInt(t *testing.T) {
	for _, route := range []string{"symbols", "between", "distinct", "null", "truth", "consumers"} {
		t.Run(route, func(t *testing.T) {
			comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE EXTENSION intarray", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE comparison_query_int(id integer,v query_int)", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO comparison_query_int VALUES(0,NULL),(1,'1'),(2,'2')", 3))
				deny := func(q, op, marker string) {
					if op == "!=" {
						op = "<>"
					}
					comparisonCheckpointAnalysis(t, p, b, q, fixture.Diagnostic{Code: "42883", Message: "operator does not exist: query_int " + op + " query_int", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: comparisonCheckpointPosition(q, marker)})
				}
				switch route {
				case "symbols":
					for _, op := range comparisonSpellings {
						for _, rhs := range []string{"'1'::query_int", "'2'::query_int", "NULL::query_int"} {
							for _, lhs := range []string{"'1'::query_int", "NULL::query_int"} {
								q := "SELECT " + lhs + " " + op + " " + rhs + " AS v"
								deny(q, op, op+" "+rhs)
							}
						}
						for _, filter := range []string{"FALSE", "l.id=0 AND r.id=0", "l.id=1 AND r.id=1", "l.id=1 AND r.id=2"} {
							q := "SELECT l.v " + op + " r.v AS v FROM comparison_query_int l CROSS JOIN comparison_query_int r WHERE " + filter
							deny(q, op, op+" r.v")
						}
					}
				case "between":
					for i, form := range comparisonBetweenForms {
						op := ">="
						if i%2 == 1 {
							op = "<"
						}
						for _, value := range []string{"'1'::query_int", "NULL::query_int"} {
							q := "SELECT " + value + " " + form + " " + value + " AND " + value + " AS v"
							deny(q, op, form)
						}
					}
				case "distinct":
					for i, form := range []string{"IS DISTINCT FROM", "IS NOT DISTINCT FROM"} {
						for _, lhs := range []string{"'1'::query_int", "NULL::query_int"} {
							for _, rhs := range []string{"'1'::query_int", "'2'::query_int", "NULL::query_int"} {
								q := "SELECT " + lhs + " " + form + " " + rhs + " AS v"
								deny(q, "=", form)
							}
						}
						for _, value := range []string{"'1'::query_int", "NULL::query_int"} {
							want := (value != "NULL::query_int") != (i == 1)
							comparisonCheckpointBool(t, p, b, value+" "+form+" NULL", want)
							comparisonCheckpointBool(t, p, b, "NULL "+form+" "+value, want)
						}
					}
				case "null":
					for i, form := range []string{"IS NULL", "IS NOT NULL", "ISNULL", "NOTNULL"} {
						rows := [][]any{{true}, {false}, {false}}
						if i%2 == 1 {
							rows = [][]any{{false}, {true}, {true}}
						}
						comparisonCheckpointRoutes(t, p, b, fixture.Select("SELECT v "+form+" AS v FROM comparison_query_int ORDER BY id", []string{"v"}, []string{"BOOL"}, rows))
					}
				case "truth":
					for _, form := range comparisonTruthForms {
						for _, value := range []string{"'1'::query_int", "NULL::query_int"} {
							q := "SELECT " + value + " " + form + " AS v"
							comparisonCheckpointAnalysis(t, p, b, q, fixture.Diagnostic{Code: "42804", Message: "argument of " + form + " must be type boolean, not type query_int", Position: "8"})
						}
					}
				case "consumers":
					for _, form := range []string{"IN", "NOT IN"} {
						op := "="
						if form == "NOT IN" {
							op = "<>"
						}
						for _, filter := range []string{"FALSE", "l.id=0 AND r.id=0", "l.id=1 AND r.id=1", "l.id=1 AND r.id=2"} {
							q := "SELECT l.v " + form + " (r.v,r.v) AS v FROM comparison_query_int l CROSS JOIN comparison_query_int r WHERE " + filter
							deny(q, op, form)
						}
						// NOT IN(subquery) is NOT(= ANY), unlike the scalar
						// list's <> conjunction; both still deny at analysis.
						for _, filter := range []string{"FALSE", "l.id=0", "l.id=1", "l.id=2"} {
							q := "SELECT l.v " + form + " (SELECT r.v FROM comparison_query_int r) AS v FROM comparison_query_int l WHERE " + filter
							deny(q, "=", form)
						}
					}
					for _, op := range comparisonSpellings {
						for _, quantifier := range []string{"ANY", "SOME", "ALL"} {
							for _, lhs := range []string{"'1'::query_int", "NULL::query_int"} {
								for _, filter := range []string{"FALSE", "id=0", "id=1", "id=2"} {
									q := "SELECT " + lhs + " " + op + " " + quantifier + "(SELECT v FROM comparison_query_int WHERE " + filter + ") AS v"
									deny(q, op, op+" "+quantifier)
								}
							}
						}
					}
				}
			})
		})
	}
}

// array_cmp resolves the element comparator only on a non-strict call. Every
// expanded BETWEEN comparison has the same unavailable leaf here. SQL AND/OR
// must still evaluate UNKNOWN's other child, so only NULL x or two NULL bounds
// avoid demand. Source order is lower first, then upper; the symmetric reverse
// arm cannot be reached successfully with a nonnull pair.
func comparisonExceptionalBetween(t *testing.T, p *fixture.Pair, b fixture.Backend, x, l, h, message string) {
	t.Helper()
	for _, spelling := range comparisonBetweenForms {
		successes, failures := 0, 0
		for a := range 3 {
			for lo := range 3 {
				for hi := range 3 {
					stage := "strict-null"
					if a != 0 && lo != 0 {
						stage = "lower-demand"
					} else if a != 0 && hi != 0 {
						stage = "upper-demand"
					}
					t.Run(fmt.Sprintf("%s/%d%d%d/%s", strings.ReplaceAll(spelling, " ", "_"), a, lo, hi, stage), func(t *testing.T) {
						q := fmt.Sprintf("SELECT %s %s %s AND %s AS v FROM comparison_values x CROSS JOIN comparison_values l CROSS JOIN comparison_values h WHERE x.id=%d AND l.id=%d AND h.id=%d", x, spelling, l, h, a, lo, hi)
						if stage == "strict-null" {
							successes++
							comparisonCheckpointRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}))
							return
						}
						failures++
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: message})
						s.SelectRows, s.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						stmt, err := b.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatalf("lookup-admitted Prepare: %v", err)
						}
						defer stmt.Close()
						held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
						for range 2 {
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						}
					})
				}
			}
		}
		if successes != 11 || failures != 16 {
			t.Fatalf("%s: strict %d demand %d, want 11/16", spelling, successes, failures)
		}
	}
}
