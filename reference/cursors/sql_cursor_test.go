package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func sqlCursorScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(context.Background(), "ROLLBACK")
			body(t, p, backend, c)
		})
	}
}

func sqlCursorStep(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func sqlCursorSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	sqlCursorStep(t, p, b, c, fixture.Exec("CREATE TABLE cursor_rows(i INTEGER NOT NULL,t TEXT)", 0))
	sqlCursorStep(t, p, b, c, fixture.Exec("INSERT INTO cursor_rows VALUES(3,'three'),(1,'one'),(2,NULL)", 3))
}

func sqlCursorDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func sqlCursorError(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query string, want fixture.Diagnostic, args ...any) {
	t.Helper()
	sqlCursorStep(t, p, b, c, fixture.Exec("SAVEPOINT cursor_error", 0))
	sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic(query, want, args...))
	sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK TO cursor_error", 0))
	sqlCursorStep(t, p, b, c, fixture.Exec("RELEASE cursor_error", 0))
}

func sqlCursorMissing(name string) fixture.Diagnostic {
	return sqlCursorDiagnostic("34000", fmt.Sprintf("cursor %q does not exist", name))
}

func sqlCursorBatch(query string, rows [][]any, pg bool) fixture.Step {
	return sqlCursorBatchMetadata(query, rows, pg, []bool{false, true})
}

func sqlCursorBatchMetadata(query string, rows [][]any, pg bool, nullable []bool) fixture.Step {
	s := fixture.Select(query, []string{"i", "t"}, []string{"INT4", "TEXT"}, rows)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		fixture.DriverBasicMetadata(t, r, pg, s.Types, nullable)
	}
	return s
}

func sqlCursorOrdered(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	sqlCursorSetup(t, p, b, c)
	sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
	// Querying a utility command independently checks its empty result schema.
	sqlCursorStep(t, p, b, c, fixture.Select("DECLARE c NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", []string{}, []string{}, [][]any{}))
	query := "FETCH FORWARD 1 FROM c"
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal("quiet FETCH prepare", err)
	}
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	sqlCursorStep(t, p, b, held, sqlCursorBatch(query, [][]any{{int64(1), "one"}}, b.PG))
	sqlCursorStep(t, p, b, held, sqlCursorBatch(query, [][]any{{int64(2), nil}}, b.PG))
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH FORWARD ALL IN c", [][]any{{int64(3), "three"}}, b.PG))
	for range 2 {
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH ALL FROM c", [][]any{}, b.PG))
	}
	sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE c", 0))
	sqlCursorError(t, p, b, c, "FETCH ALL FROM c", sqlCursorMissing("c"))
	sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE c NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
	sqlCursorStep(t, p, b, c, fixture.Exec("COMMIT", 0))
	sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM c", sqlCursorMissing("c")))
	sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
	sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE c NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
	sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM c", [][]any{{int64(1), "one"}}, b.PG))
	sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
}

func TestReferenceSQLCursorOrderedBatches(t *testing.T) { sqlCursorScenario(t, sqlCursorOrdered) }

func TestReferenceSQLCursorFeatureSmoke(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorOrdered(t, p, b, c)
		sqlCursorDemand(t, p, b, c)
	})
}

func TestReferenceSQLCursorEmptyAndMetadata(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		sqlCursorStep(t, p, b, c, fixture.Exec("CREATE TABLE cursor_empty(i INTEGER NOT NULL,t TEXT)", 0))
		sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
		sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE empty_table NO SCROLL CURSOR FOR SELECT i,t FROM cursor_empty", 0))
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH ALL FROM empty_table", [][]any{}, b.PG))
		sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE zero_width NO SCROLL CURSOR FOR SELECT FROM cursor_rows", 0))
		sqlCursorStep(t, p, b, c, fixture.Select("FETCH 1 FROM zero_width", []string{}, []string{}, [][]any{{}}))
		sqlCursorStep(t, p, b, c, fixture.Exec("FETCH ALL FROM zero_width", 2))
		sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM zero_width", []string{}, []string{}, [][]any{}))
		for _, predicate := range []string{"FALSE", "i > 99"} {
			sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE e NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows WHERE "+predicate, 0))
			sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH ALL FROM e", [][]any{}, b.PG))
			sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE e", 0))
		}
		sqlCursorStep(t, p, b, c, fixture.Prepared(fixture.Exec("DECLARE e NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows WHERE i = $1", 0, nil)))
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH ALL FROM e", [][]any{}, b.PG))
		sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE e", 0))
		sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE v NO SCROLL CURSOR FOR VALUES (1,'a'::TEXT),(2,NULL::TEXT)", 0))
		sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM v", []string{"column1", "column2"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}}))
		sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE aliases NO SCROLL CURSOR FOR SELECT i AS x,i AS x,t AS label FROM cursor_rows ORDER BY i", 0))
		sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM aliases", []string{"x", "x", "label"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "one"}, {int64(2), int64(2), nil}, {int64(3), int64(3), "three"}}))
		sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE oidtext NO SCROLL CURSOR FOR SELECT 'cursor_rows'::REGCLASS AS r", 0))
		sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM oidtext", []string{"r"}, []string{"REGCLASS"}, [][]any{{"cursor_rows"}}))
		sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE ALL", 0))
	})
}
