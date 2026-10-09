package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceExtremaDiagnostics(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE extrema_errors(v INTEGER)", 0))
		for _, tc := range []struct{ query, message string }{
			{"SELECT min(TRUE) FROM extrema_errors", "function min(boolean) does not exist"},
			{"SELECT max('01234567-89ab-cdef-0123-456789abcdef'::uuid) FROM extrema_errors", "function max(uuid) does not exist"},
			{"SELECT min() FROM extrema_errors", "function min() does not exist"},
			{"SELECT min(*) FROM extrema_errors", "function min() does not exist"},
			{"SELECT min(1,2) FROM extrema_errors", "function min(integer, integer) does not exist"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				want := controlsReferenceDiagnostic("42883", tc.message)
				want.Position = "8"
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				s := referenceErrorDiagnostic(tc.query, want)
				s.selectRows = true
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, conn, recorder, s)
				s = referencePrepared(s)
				s.errorStage = "prepare"
				referenceRunStep(t, p.ctx, conn, recorder, s)
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
				want := controlsReferenceDiagnostic(tc.code, tc.message)
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
				want := controlsReferenceDiagnostic(tc.code, tc.message)
				want.Position = "48"
				if tc.code == "42883" {
					want.Hint = extremaSignatureHint
				}
				if !pg {
					want = controlsReferenceDiagnostic("0A000", "INSERT suffix is unsupported")
					want.Position = stringAggPosition(tc.query, "RETURNING", false)
				}
				for _, queryMode := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						s := referenceErrorDiagnostic(tc.query, want)
						s.selectRows = queryMode
						s.errorStage = "execute"
						if prepared {
							s = referencePrepared(s)
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, conn, recorder, s)
						referenceRunStep(t, p.ctx, conn, recorder, referenceSelect("SELECT count(*) FROM extrema_errors", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
				}
				if !pg {
					// Parsing the complete input must not publish a valid prefix.
					query := "INSERT INTO extrema_errors VALUES(9); " + tc.query
					prefixWant := controlsReferenceDiagnostic("0A000", "INSERT suffix is unsupported")
					prefixWant.Position = stringAggPosition(query, "RETURNING", false)
					for _, prepared := range []bool{false, true} {
						s := referenceErrorDiagnostic(query, prefixWant)
						s.errorStage = "execute"
						if prepared {
							s = referencePrepared(s)
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, conn, recorder, s)
						referenceRunStep(t, p.ctx, conn, recorder, referenceSelect("SELECT count(*) FROM extrema_errors", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
				}
			})
		}
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE DOMAIN extrema_bool AS BOOLEAN", 0))
		for _, path := range []string{"public", "pg_catalog"} {
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET search_path TO "+path, 0))
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
						want := controlsReferenceDiagnostic("42883", "function "+aggregate+"("+diagnosticName+") does not exist")
						want.Position, want.Hint = "8", extremaSignatureHint
						extremaDiagnostic(t, p, conn, recorder, "SELECT "+aggregate+"(NULL::"+domain+") FROM public.extrema_errors", want, "prepare")
					})
				}
			}
		}
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET search_path TO public", 0))
	})
}

func TestReferenceExtremaCollation(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("CREATE TABLE extrema_coll(a TEXT COLLATE \"C\",b TEXT COLLATE \"POSIX\")", 0))
		query := "SELECT min(COALESCE(a,b)),max(COALESCE(a,b)) FROM extrema_coll"
		check := func(name string, rows [][]any) {
			t.Run(name, func(t *testing.T) {
				extremaBoth(t, p, conn, recorder, referenceSelect(query, []string{"min", "max"}, []string{"TEXT", "TEXT"}, rows))
			})
		}
		check("empty", [][]any{{nil, nil}})
		run(referenceExec("INSERT INTO extrema_coll VALUES(NULL,NULL)", 1))
		check("all_null", [][]any{{nil, nil}})
		run(referenceExec("INSERT INTO extrema_coll VALUES('a','b')", 1))
		check("one_eligible", [][]any{{"a", "a"}})
		run(referenceExec("INSERT INTO extrema_coll VALUES('c','d')", 1))
		t.Run("two_eligible", func(t *testing.T) {
			want := controlsReferenceDiagnostic("42P22", "could not determine which collation to use for string comparison")
			want.Hint = "Use the COLLATE clause to set the collation explicitly."
			extremaDiagnostic(t, p, conn, recorder, query, want, "execute")
		})
		extremaBoth(t, p, conn, recorder, referenceSelect("SELECT min(COALESCE(a,b) COLLATE \"C\"),max(COALESCE(a,b) COLLATE \"C\") FROM extrema_coll", []string{"min", "max"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "c"}}))
	})
}

const extremaSignatureHint = "No function matches the given name and argument types. You might need to add explicit type casts."

func extremaDiagnostic(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, query string, want referenceDiagnostic, preparedStage string) {
	t.Helper()
	s := referenceErrorDiagnostic(query, want)
	s.selectRows = true
	s.errorStage = "execute"
	referenceRunStep(t, p.ctx, conn, recorder, s)
	s = referencePrepared(s)
	s.errorStage = preparedStage
	referenceRunStep(t, p.ctx, conn, recorder, s)
}
