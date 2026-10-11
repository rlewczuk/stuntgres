package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtremaDiagnostics(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE extrema_errors(v INTEGER)", 0))
		for _, tc := range []struct{ query, message string }{
			{"SELECT min(TRUE) FROM extrema_errors", "function min(boolean) does not exist"},
			{"SELECT max('01234567-89ab-cdef-0123-456789abcdef'::uuid) FROM extrema_errors", "function max(uuid) does not exist"},
			{"SELECT min() FROM extrema_errors", "function min() does not exist"},
			{"SELECT min(*) FROM extrema_errors", "function min() does not exist"},
			{"SELECT min(1,2) FROM extrema_errors", "function min(integer, integer) does not exist"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				want := fixture.ControlsReferenceDiagnostic("42883", tc.message)
				want.Position = "8"
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				s := fixture.ErrorDiagnostic(tc.query, want)
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, conn, recorder, s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, conn, recorder, s)
			})
		}
		for _, tc := range []struct {
			query, code, message, position string
		}{
			{"SELECT min(missing,1) FROM extrema_errors", "42703", `column "missing" does not exist`, "12"},
			{"SELECT missing_schema.min(v) FROM extrema_errors", "3F000", `schema "missing_schema" does not exist`, "8"},
			{"SELECT public.max(v) FROM extrema_errors", "42883", "function public.max(integer) does not exist", "8"},
			{"SELECT 'é',public.min(TRUE) FROM extrema_errors GROUP BY 1", "42883", "function public.min(boolean) does not exist", "12"},
			{"SELECT min(max(v)) FROM extrema_errors", "42803", "aggregate function calls cannot be nested", "12"},
			{"SELECT min(max(TRUE)) FROM extrema_errors", "42883", "function max(boolean) does not exist", "12"},
			{"SELECT min(" + strings.Repeat("1,", 100) + "1) FROM extrema_errors", "54023", "cannot pass more than 100 arguments to a function", "8"},
			{"SELECT v FROM extrema_errors WHERE min(v)>0", "42803", "aggregate functions are not allowed in WHERE", "36"},
			{"SELECT v FROM extrema_errors GROUP BY min(v)", "42803", "aggregate functions are not allowed in GROUP BY", "39"},
			{"SELECT a.v FROM extrema_errors a JOIN extrema_errors b ON min(a.v)=b.v", "42803", "aggregate functions are not allowed in JOIN conditions", "59"},
			{"SELECT v FROM extrema_errors LIMIT min(v)", "42803", "aggregate functions are not allowed in LIMIT", "36"},
			{"SELECT v FROM extrema_errors OFFSET max(v)", "42803", "aggregate functions are not allowed in OFFSET", "37"},
			{"CREATE TABLE extrema_check(v INTEGER CHECK(min(v)>0))", "42803", "aggregate functions are not allowed in check constraints", "44"},
			{"SELECT v,min(v) FROM extrema_errors WHERE FALSE", "42803", `column "extrema_errors.v" must appear in the GROUP BY clause or be used in an aggregate function`, "8"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Position = tc.position
				if tc.code == "42883" {
					want.Hint = extremaSignatureHint
				}
				prepareStage := "prepare"
				if strings.HasPrefix(tc.query, "CREATE TABLE") {
					prepareStage = "execute"
				}
				extremaDiagnostic(t, p, conn, recorder, tc.query, want, prepareStage)
			})
		}
		// Native parsing rejects the suffix before RETURNING expression analysis.
		// PostgreSQL independently diagnoses the aggregate clause/signature.
		for _, tc := range []struct{ query, code, message string }{
			{"INSERT INTO extrema_errors VALUES(1) RETURNING min(v)", "42803", "aggregate functions are not allowed in RETURNING"},
			{"INSERT INTO extrema_errors VALUES(1) RETURNING min(TRUE)", "42883", "function min(boolean) does not exist"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Position = "48"
				if tc.code == "42883" {
					want.Hint = extremaSignatureHint
				}
				if !pg {
					want = fixture.ControlsReferenceDiagnostic("0A000", "INSERT suffix is unsupported")
					want.Position = fixture.StringAggPosition(tc.query, "RETURNING", false)
				}
				for _, queryMode := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(tc.query, want)
						s.SelectRows = queryMode
						s.ErrorStage = "execute"
						if prepared {
							s = fixture.Prepared(s)
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, conn, recorder, s)
						fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Select("SELECT count(*) FROM extrema_errors", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
				}
				if !pg {
					// Parsing the complete input must not publish a valid prefix.
					query := "INSERT INTO extrema_errors VALUES(9); " + tc.query
					prefixWant := fixture.ControlsReferenceDiagnostic("0A000", "INSERT suffix is unsupported")
					prefixWant.Position = fixture.StringAggPosition(query, "RETURNING", false)
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(query, prefixWant)
						s.ErrorStage = "execute"
						if prepared {
							s = fixture.Prepared(s)
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, conn, recorder, s)
						fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Select("SELECT count(*) FROM extrema_errors", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
				}
			})
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE DOMAIN extrema_bool AS BOOLEAN", 0))
		for _, path := range []string{"public", "pg_catalog"} {
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO "+path, 0))
			for _, aggregate := range []string{"min", "max"} {
				for _, domain := range []string{"extrema_bool", "public.extrema_bool"} {
					if path == "pg_catalog" && domain == "extrema_bool" {
						continue // Only qualified input resolves the hidden domain.
					}
					t.Run(path+"/"+aggregate+"/"+domain, func(t *testing.T) {
						diagnosticName := "extrema_bool"
						if path == "pg_catalog" {
							diagnosticName = "public.extrema_bool"
						}
						want := fixture.ControlsReferenceDiagnostic("42883", "function "+aggregate+"("+diagnosticName+") does not exist")
						want.Position, want.Hint = "8", extremaSignatureHint
						extremaDiagnostic(t, p, conn, recorder, "SELECT "+aggregate+"(NULL::"+domain+") FROM public.extrema_errors", want, "prepare")
					})
				}
			}
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO public", 0))
	})
}

func TestReferenceExtremaCollation(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE extrema_coll(a TEXT COLLATE \"C\",b TEXT COLLATE \"POSIX\")", 0))
		query := "SELECT min(COALESCE(a,b)),max(COALESCE(a,b)) FROM extrema_coll"
		check := func(name string, rows [][]any) {
			t.Run(name, func(t *testing.T) {
				extremaBoth(t, p, conn, recorder, fixture.Select(query, []string{"min", "max"}, []string{"TEXT", "TEXT"}, rows))
			})
		}
		check("empty", [][]any{{nil, nil}})
		run(fixture.Exec("INSERT INTO extrema_coll VALUES(NULL,NULL)", 1))
		check("all_null", [][]any{{nil, nil}})
		run(fixture.Exec("INSERT INTO extrema_coll VALUES('a','b')", 1))
		check("one_eligible", [][]any{{"a", "a"}})
		run(fixture.Exec("INSERT INTO extrema_coll VALUES('c','d')", 1))
		t.Run("two_eligible", func(t *testing.T) {
			want := fixture.ControlsReferenceDiagnostic("42P22", "could not determine which collation to use for string comparison")
			want.Hint = "Use the COLLATE clause to set the collation explicitly."
			extremaDiagnostic(t, p, conn, recorder, query, want, "execute")
		})
		extremaBoth(t, p, conn, recorder, fixture.Select("SELECT min(COALESCE(a,b) COLLATE \"C\"),max(COALESCE(a,b) COLLATE \"C\") FROM extrema_coll", []string{"min", "max"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "c"}}))
	})
}

const extremaSignatureHint = "No function matches the given name and argument types. You might need to add explicit type casts."

func extremaDiagnostic(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, query string, want fixture.Diagnostic, preparedStage string) {
	t.Helper()
	s := fixture.ErrorDiagnostic(query, want)
	s.SelectRows = true
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, conn, recorder, s)
	s = fixture.Prepared(s)
	s.ErrorStage = preparedStage
	fixture.RunStep(t, p.Ctx, conn, recorder, s)
}
