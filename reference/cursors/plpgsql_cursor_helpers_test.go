package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func plCursorRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	sqlCursorStep(t, p, b, c, fixture.Exec("CREATE TABLE pc_rows(i integer,t text)", 0))
	sqlCursorStep(t, p, b, c, fixture.Exec("INSERT INTO pc_rows VALUES(10,'ten'),(20,'twenty'),(30,'thirty')", 3))
}

func plCursorFunction(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, name, args, typ, body string) {
	t.Helper()
	sqlCursorStep(t, p, b, c, fixture.Exec("CREATE FUNCTION "+name+"("+args+") RETURNS "+typ+" LANGUAGE plpgsql AS $$"+body+"$$", 0))
}

// Direct, fresh Prepare, and two executions of one genuinely held statement.
func plCursorValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query, typ string, value any, args ...any) {
	t.Helper()
	s := fixture.Select(query, []string{"v"}, []string{typ}, [][]any{{value}}, args...)
	sqlCursorStep(t, p, b, c, s)
	sqlCursorStep(t, p, b, c, fixture.Prepared(s))
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	for range 2 {
		sqlCursorStep(t, p, b, held, s)
	}
}

type plCursorCase struct {
	name, declarations, statements, result, typ string
	want                                        any
}

func plCursorCases(t *testing.T, cases []plCursorCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorRows(t, p, b, c)
				body := "DECLARE " + tc.declarations + " BEGIN " + tc.statements + " RETURN " + tc.result + "; END"
				plCursorFunction(t, p, b, c, "pc_case", "", tc.typ, body)
				plCursorValue(t, p, b, c, "SELECT pc_case() AS v", map[string]string{"integer": "INT4", "bigint": "INT8", "boolean": "BOOL", "text": "TEXT", "refcursor": "REFCURSOR"}[tc.typ], tc.want)
			})
		})
	}
}

func plCursorRuntime(code, message, signature, action string) fixture.Diagnostic {
	d := sqlCursorDiagnostic(code, message)
	d.Context = "PL/pgSQL function " + signature + " line 1 at " + action
	return d
}

// Full diagnostic expectations include the absence of optional fields.
func plCursorError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, want fixture.Diagnostic, queryMode, inTx bool, preparedPGWant ...fixture.Diagnostic) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		func() {
			if inTx {
				sqlCursorStep(t, p, b, c, fixture.Exec("SAVEPOINT pc_error", 0))
				defer func() {
					sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK TO pc_error", 0))
					sqlCursorStep(t, p, b, c, fixture.Exec("RELEASE pc_error", 0))
				}()
			}
			var stmt *sql.Stmt
			var err error
			stage := "execute"
			if prepared {
				stmt, err = c.PrepareContext(p.Ctx, query)
				if err != nil {
					stage = "prepare"
				}
				if stmt != nil {
					defer stmt.Close()
				}
			}
			if err == nil {
				if queryMode {
					var rows *sql.Rows
					if stmt != nil {
						rows, err = stmt.QueryContext(p.Ctx)
					} else {
						rows, err = c.QueryContext(p.Ctx, query)
					}
					if rows != nil {
						rows.Close()
						t.Fatal("error query exposed rows")
					}
				} else {
					var result sql.Result
					if stmt != nil {
						result, err = stmt.ExecContext(p.Ctx)
					} else {
						result, err = c.ExecContext(p.Ctx, query)
					}
					if result != nil {
						t.Fatal("error command exposed result")
					}
				}
			}
			if err == nil {
				t.Fatal("expected diagnostic")
			}
			got := fixture.ExtractDiagnostic(t, err)
			// Public Prepare must succeed for reached PL runtime failures, and
			// CREATE FUNCTION validates its body at execution, not Prepare.
			if stage != "execute" {
				t.Errorf("stage=%s, want execute", stage)
			}
			expected := want
			// Like fixture.Step.PostgresDiagnostic, this declares the pinned
			// oracle's incidental cached-expression trace independently rather
			// than requiring native execution to reproduce PostgreSQL's cache.
			if b.PG && prepared && len(preparedPGWant) != 0 {
				expected = preparedPGWant[0]
			}
			if got != expected {
				t.Errorf("diagnostic got=%#v want=%#v", got, expected)
			}
		}()
	}
}

func plCursorHelpers(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	plCursorFunction(t, p, b, c, "pc_open", "p refcursor", "refcursor", "BEGIN OPEN p SCROLL FOR SELECT i FROM pc_rows ORDER BY i; RETURN p; END")
	plCursorFunction(t, p, b, c, "pc_take", "p refcursor", "integer", "DECLARE x integer; BEGIN FETCH NEXT FROM p INTO x; RETURN x; END")
	plCursorFunction(t, p, b, c, "pc_close", "p refcursor", "void", "BEGIN CLOSE p; END")
}

func plCursorMissing(name string) fixture.Diagnostic {
	return plCursorRuntime("34000", fmt.Sprintf("cursor %q does not exist", name), "pc_take(refcursor)", "FETCH")
}
