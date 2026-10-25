package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func ftsQueryFunctionsShapeError() fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("22023", "ts_rewrite query must return two tsquery columns")
}

// Complete records and phases were resolved against the pinned PG18.6 oracle.
// Unset optional fields are asserted absent by ErrorDiagnostic.
func ftsQueryFunctionsDiagnosticRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, query string, want fixture.Diagnostic, eager bool, args ...any) {
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
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
	}
	if !eager {
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal("runtime failure must not fail Prepare:", err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
		for range 2 {
			for _, queryMode := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(query, want, args...)
				s.SelectRows = queryMode
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		}
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT ord,t,s FROM fts_qf_errors ORDER BY ord", []string{"ord", "t", "s"}, []string{"INT4", "TSQUERY", "TSQUERY"}, [][]any{{int64(1), "'a'", "'b'"}}))
}

func TestReferenceFTSQueryFunctionsDiagnostics(t *testing.T) {
	// Internal positions come from parse/analysis; open-time errors use SQL
	// statement context, while fetch-time errors carry neither annotation.
	for _, tc := range []struct {
		name, inner, code, message string
		position                   int
		context                    bool
	}{
		{"one_column", "SELECT 'a'::tsquery", "22023", "ts_rewrite query must return two tsquery columns", 0, false},
		{"three_columns", "SELECT 'a'::tsquery,'b'::tsquery,'c'::tsquery", "22023", "ts_rewrite query must return two tsquery columns", 0, false},
		{"text_columns", "SELECT 'a'::text,'b'::text", "22023", "ts_rewrite query must return two tsquery columns", 0, false},
		{"zero_wrong_shape", "SELECT 'a'::tsquery,'b'::text WHERE FALSE", "22023", "ts_rewrite query must return two tsquery columns", 0, false},
		{"domain_shape", "SELECT 'a'::fts_qf_domain,'b'::tsquery", "22023", "ts_rewrite query must return two tsquery columns", 0, false},
		// SELECT without a target list is valid SQL with zero output columns.
		{"zero_columns", "SELECT", "22023", "ts_rewrite query must return two tsquery columns", 0, false},
		{"missing_column", "SELECT missing::tsquery, 'b'::tsquery", "42703", `column "missing" does not exist`, 8, false},
		{"missing_relation", "SELECT t,s FROM fts_qf_absent", "42P01", `relation "fts_qf_absent" does not exist`, 17, false},
		{"inner_parameter", "SELECT $1::tsquery,'b'::tsquery", "42P02", "there is no parameter $1", 8, false},
		{"multiple", "SELECT 'a'::tsquery,'b'::tsquery; SELECT 'b'::tsquery,'c'::tsquery", "42P11", "cannot open multi-query plan as cursor", 0, false},
		{"empty", "", "42P11", "cannot open multi-query plan as cursor", 0, false},
		{"comment", "/* only a comment */", "42P11", "cannot open multi-query plan as cursor", 0, false},
		{"insert", "INSERT INTO fts_qf_errors VALUES(2,'a','z')", "42P11", "cannot open INSERT query as cursor", 0, false},
		{"insert_returning", "INSERT INTO fts_qf_errors VALUES(2,'a','z') RETURNING t,s", "0A000", "INSERT is not allowed in a non-volatile function", 0, true},
		{"modifying_cte", "WITH changed AS (INSERT INTO fts_qf_errors VALUES(2,'a','z') RETURNING t,s) SELECT t,s FROM changed", "0A000", "SELECT is not allowed in a non-volatile function", 0, true},
		{"row_lock", "SELECT t,s FROM fts_qf_errors FOR UPDATE", "0A000", "SELECT FOR UPDATE is not allowed in a non-volatile function", 0, true},
		{"constant_division", "SELECT 'a'::tsquery,'b'::tsquery WHERE 1/0=0", "22012", "division by zero", 0, true},
		{"row_division", "SELECT t,s FROM fts_qf_errors WHERE 1/(ord-1)=0", "22012", "division by zero", 0, false},
		{"constant_cast", "SELECT 'a &'::tsquery,'b'::tsquery", "42601", `no operand in tsquery: "a &"`, 8, false},
		{"row_cast", "SELECT (t::text || ' &')::tsquery,s FROM fts_qf_errors", "42601", `no operand in tsquery: "'a' &"`, 0, false},
		{"suffix", "SELECT 'a'::tsquery,'b'::tsquery; garbage", "42601", `syntax error at or near "garbage"`, 35, false},
		{"unicode_position", "/*é猫*/ SELECT missing::tsquery,'b'::tsquery", "42703", `column "missing" does not exist`, 15, false},
		{"multi_analysis_precedence", "SELECT 'a'::tsquery,'b'::tsquery; SELECT missing FROM fts_qf_errors", "42703", `column "missing" does not exist`, 42, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ftsQueryFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE fts_qf_errors(ord integer,t tsquery,s tsquery)", 0))
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("INSERT INTO fts_qf_errors VALUES(1,'a','b')", 1))
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE DOMAIN fts_qf_domain AS tsquery", 0))
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				if tc.position != 0 {
					d.InternalPosition = fixture.Position(tc.position)
					d.InternalQuery = tc.inner
				}
				if tc.context {
					d.Context = "SQL statement \"" + tc.inner + "\""
				}
				ftsQueryFunctionsDiagnosticRoutes(t, p, b, conn, "SELECT ts_rewrite('a'::tsquery,"+ftsTypesQuote(tc.inner)+")", d, false)
			})
		})
	}
}

func TestReferenceFTSQueryFunctionsOuterDiagnostics(t *testing.T) {
	for i, tc := range []struct {
		query, code, message string
		position             int
		eager                bool
		args                 []any
	}{
		{"SELECT numnode()", "42883", "function numnode() does not exist", 8, true, nil},
		{"SELECT querytree(1)", "42883", "function querytree(integer) does not exist", 8, true, nil},
		{"SELECT ts_rewrite('a'::tsquery)", "42883", "function ts_rewrite(tsquery) does not exist", 8, true, nil},
		{"SELECT ts_rewrite('a'::tsquery,'b'::text,'c'::tsquery)", "42883", "function ts_rewrite(tsquery, text, tsquery) does not exist", 8, true, nil},
		{"SELECT ts_rewrite(source => 'a'::tsquery, target => 'b'::tsquery, substitute => 'c'::tsquery)", "42883", "function ts_rewrite(source => tsquery, target => tsquery, substitute => tsquery) does not exist", 8, true, nil},
		{"SELECT ts_rewrite('a','b')", "42601", `syntax error at or near "b"`, 0, false, nil},
		{"SELECT numnode('a &'::tsquery)", "42601", `no operand in tsquery: "a &"`, 16, true, nil},
		{"SELECT querytree($1::tsquery)", "42601", `no operand in tsquery: "a &"`, 0, false, []any{"a &"}},
		{"SELECT ts_rewrite($1::tsquery,'SELECT $1::tsquery,\"s\" FROM fts_qf_errors')", "42P02", "there is no parameter $1", 0, false, []any{"a"}},
	} {
		t.Run(fmt.Sprintf("outer_%d", i), func(t *testing.T) {
			ftsQueryFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE fts_qf_errors(ord integer,t tsquery,s tsquery)", 0))
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("INSERT INTO fts_qf_errors VALUES(1,'a','b')", 1))
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = fixture.Position(tc.position)
				if tc.code == "42883" {
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				}
				switch i {
				case 5:
					d.InternalQuery = "b"
					d.InternalPosition = "1"
				case 7:
					d.Context = "unnamed portal parameter $1 = '...'"
				case 8:
					d.InternalQuery = "SELECT $1::tsquery,\"s\" FROM fts_qf_errors"
					d.InternalPosition = "8"
				}
				ftsQueryFunctionsDiagnosticRoutes(t, p, b, conn, tc.query, d, tc.eager, tc.args...)
			})
		})
	}
}
