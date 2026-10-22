package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations below combine independently specified SQL contracts with the
// root's pinned PostgreSQL18.6 diagnostic/phase observations (oracle round1).
func ftsOpsDiagnosticRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, query string, want fixture.Diagnostic, eager bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		for _, queryMode := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(query, want, args...)
			s.Prepared = prepared
			s.SelectRows = queryMode
			s.ErrorStage = "execute"
			if prepared && eager {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	}
	if eager {
		return
	}
	stmt, err := b.DB.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal("runtime failure must not fail Prepare:", err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
	for range 2 {
		for _, queryMode := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(query, want, args...)
			s.SelectRows = queryMode
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	}
}

func ftsOpsDiagnosticSuccess(t *testing.T, p *fixture.Pair, b fixture.Backend, query, column, typ string, want any, args ...any) {
	t.Helper()
	s := fixture.Select(query, []string{column}, []string{typ}, [][]any{{want}}, args...)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	if _, err := b.DB.ExecContext(p.Ctx, query, args...); err != nil {
		t.Fatal(err)
	}
	stmt, err := b.DB.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
	for range 2 {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		if _, err := stmt.ExecContext(p.Ctx, args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceFTSOpsDiagnostics(t *testing.T) {
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE fts_ops_errors(a tsquery,b tsquery,d integer)", 0))
		for i, tc := range []struct {
			query, signature, position string
			unary                      bool
		}{
			{"SELECT 'a'::tsquery && 1", "tsquery && integer", "21", false},
			{"SELECT 'a'::tsquery || 1", "tsquery || integer", "21", false},
			{"SELECT !!1", "!! integer", "8", true},
			{"SELECT 'a'::tsquery <-> 1", "tsquery <-> integer", "21", false},
			{"SELECT 'a'::tsquery @> 1", "tsquery @> integer", "21", false},
			{"SELECT 'a'::tsquery <@ 1", "tsquery <@ integer", "21", false},
			{"SELECT 'a'::tsvector || 1", "tsvector || integer", "22", false},
			{"SELECT 'a'::tsvector || 'b'::tsquery", "tsvector || tsquery", "22", false},
			{"SELECT 'a'::tsquery || 'b'::tsvector", "tsquery || tsvector", "21", false},
			{"SELECT 'a'::text && 'b'::text", "text && text", "18", false},
			{"SELECT 'a'::text <-> 'b'::text", "text <-> text", "18", false},
			{"SELECT !!'a'::text", "!! text", "8", true},
			{"SELECT 'a'::tsquery && 'b'::text", "tsquery && text", "21", false},
			{"SELECT 'a'::tsquery <-> 'b'::text", "tsquery <-> text", "21", false},
			{"SELECT 'a'::tsquery @> 'b'::text", "tsquery @> text", "21", false},
			{"SELECT 'a'::tsquery <@ 'b'::text", "tsquery <@ text", "21", false},
			{"/*é*/ SELECT 'a'::tsquery && 1", "tsquery && integer", "27", false},
			{"/*é*/ SELECT 'a'::tsquery OPERATOR(pg_catalog.&&) 1", "tsquery pg_catalog.&& integer", "27", false},
		} {
			t.Run(fmt.Sprintf("operator_%02d", i), func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+tc.signature)
				d.Position = tc.position
				d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				if tc.unary {
					d.Hint = "No operator matches the given name and argument type. You might need to add an explicit type cast."
				}
				ftsOpsDiagnosticRoutes(t, p, b, tc.query, d, true)
			})
		}
		for _, op := range []string{"&&", "<->", "@>", "<@"} {
			t.Run("ambiguous_"+op, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42725", "operator is not unique: unknown "+op+" unknown")
				d.Position = "12"
				d.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
				ftsOpsDiagnosticRoutes(t, p, b, "SELECT 'a' "+op+" 'b'", d, true)
			})
		}
		for i, tc := range []struct{ query, signature, position string }{
			{"SELECT tsquery_phrase()", "tsquery_phrase()", "8"},
			{"SELECT tsquery_phrase('a')", "tsquery_phrase(unknown)", "8"},
			{"SELECT tsquery_phrase('a','b',1,2)", "tsquery_phrase(unknown, unknown, integer, integer)", "8"},
			{"SELECT tsquery_phrase('a'::text,'b'::text)", "tsquery_phrase(text, text)", "8"},
			{"SELECT tsquery_phrase('a','b','1'::text)", "tsquery_phrase(unknown, unknown, text)", "8"},
			{"SELECT tsquery_phrase('a','b',TRUE)", "tsquery_phrase(unknown, unknown, boolean)", "8"},
			{"SELECT tsquery_phrase('a','b',1::bigint)", "tsquery_phrase(unknown, unknown, bigint)", "8"},
			{"SELECT tsquery_phrase('a','b',1::numeric)", "tsquery_phrase(unknown, unknown, numeric)", "8"},
			{"SELECT tsquery_phrase('a','b',2147483648)", "tsquery_phrase(unknown, unknown, bigint)", "8"},
			{"/*é*/ SELECT pg_catalog.tsquery_phrase('a','b',TRUE)", "pg_catalog.tsquery_phrase(unknown, unknown, boolean)", "14"},
		} {
			t.Run(fmt.Sprintf("function_%02d", i), func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
				d.Position = tc.position
				d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				ftsOpsDiagnosticRoutes(t, p, b, tc.query, d, true)
			})
		}
		for i, tc := range []struct{ query, code, message, position string }{
			{"SELECT tsquery_phrase('a','b','1.5')", "22P02", "invalid input syntax for type integer: \"1.5\"", "31"},
			{"SELECT tsquery_phrase('a','b','bad')", "22P02", "invalid input syntax for type integer: \"bad\"", "31"},
			{"SELECT tsquery_phrase('a','b','2147483648')", "22003", "value \"2147483648\" is out of range for type integer", "31"},
			{"SELECT tsquery_phrase(NULL::tsquery,'a &',1)", "42601", "no operand in tsquery: \"a &\"", "37"},
			{"SELECT tsquery_phrase(NULL::tsquery,'b','bad')", "22P02", "invalid input syntax for type integer: \"bad\"", "41"},
			{"SELECT tsquery_phrase('a &','b',1) FROM fts_ops_errors WHERE FALSE", "42601", "no operand in tsquery: \"a &\"", "23"},
		} {
			t.Run(fmt.Sprintf("input_%02d", i), func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = tc.position
				ftsOpsDiagnosticRoutes(t, p, b, tc.query, d, true)
			})
		}
		for i, query := range []string{
			"SELECT tsquery_phrase('a','b',-1)",
			"SELECT tsquery_phrase('','b',16385)",
			"SELECT tsquery_phrase('a','b',16385) FROM fts_ops_errors",
			"SELECT tsquery_phrase('a','b',16385) FROM fts_ops_errors WHERE FALSE",
			"/*é*/ SELECT pg_catalog.tsquery_phrase('a','b',16385)",
		} {
			t.Run(fmt.Sprintf("distance_%02d", i), func(t *testing.T) { ftsOpsDiagnosticRoutes(t, p, b, query, ftsOpsDistanceError(), false) })
		}
		for _, tc := range []struct {
			query, column, typ string
			want               any
		}{
			{"SELECT !!'a'", "?column?", "TSQUERY", "!'a'"},
			{"SELECT 'a' || 'b'", "?column?", "TEXT", "ab"},
			{"SELECT 'a'::tsquery || 'b'::text", "?column?", "TEXT", "'a'b"},
			{"SELECT 'a'::tsvector || 'b'::text", "?column?", "TEXT", "'a'b"},
			{"SELECT tsquery_phrase('a','b','1')", "tsquery_phrase", "TSQUERY", "'a' <-> 'b'"},
			{"SELECT tsquery_phrase(NULL::tsquery,'b',16385)", "tsquery_phrase", "TSQUERY", nil},
		} {
			t.Run(tc.query, func(t *testing.T) { ftsOpsDiagnosticSuccess(t, p, b, tc.query, tc.column, tc.typ, tc.want) })
		}
		boundQuery := "SELECT tsquery_phrase($1::tsquery,$2::tsquery,$3::int4)"
		for _, tc := range []struct {
			label                  string
			args                   []any
			code, message, context string
		}{
			{"bound_distance", []any{"a", "b", 16385}, "22023", ftsOpsDistanceError().Message, ""},
			{"bound_empty_distance", []any{"", "b", -1}, "22023", ftsOpsDistanceError().Message, ""},
			{"bound_bad_query", []any{"a &", "b", 1}, "42601", "no operand in tsquery: \"a &\"", "unnamed portal parameter $1 = '...'"},
			{"bound_bad_int", []any{"a", "b", "bad"}, "22P02", "invalid input syntax for type integer: \"bad\"", "unnamed portal parameter $3 = '...'"},
			{"bound_int_overflow", []any{"a", "b", "2147483648"}, "22003", "value \"2147483648\" is out of range for type integer", "unnamed portal parameter $3 = '...'"},
		} {
			t.Run(tc.label, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Context = tc.context
				ftsOpsDiagnosticRoutes(t, p, b, boundQuery, d, false, tc.args...)
			})
		}
		t.Run("bound_null_distance", func(t *testing.T) {
			ftsOpsDiagnosticSuccess(t, p, b, boundQuery, "tsquery_phrase", "TSQUERY", nil, nil, "b", 16385)
		})
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO fts_ops_errors VALUES('a','b',16385)", 1))
		t.Run("column_runtime", func(t *testing.T) {
			ftsOpsDiagnosticRoutes(t, p, b, "SELECT tsquery_phrase(a,b,d) FROM fts_ops_errors", ftsOpsDistanceError(), false)
		})
	})
}
