package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func sqlCursorScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(context.Background(), "ROLLBACK")
			body(t, p, backend, c)
		})
	}
}

func sqlCursorStep(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor, s referenceStep) {
	t.Helper()
	referenceRunStep(t, p.ctx, c, b.recorder, s)
}

func sqlCursorSetup(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor) {
	t.Helper()
	sqlCursorStep(t, p, b, c, referenceExec("CREATE TABLE cursor_rows(i INTEGER NOT NULL,t TEXT)", 0))
	sqlCursorStep(t, p, b, c, referenceExec("INSERT INTO cursor_rows VALUES(3,'three'),(1,'one'),(2,NULL)", 3))
}

func sqlCursorDiagnostic(code, message string) referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func sqlCursorError(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor, query string, want referenceDiagnostic, args ...any) {
	t.Helper()
	sqlCursorStep(t, p, b, c, referenceExec("SAVEPOINT cursor_error", 0))
	sqlCursorStep(t, p, b, c, referenceErrorDiagnostic(query, want, args...))
	sqlCursorStep(t, p, b, c, referenceExec("ROLLBACK TO cursor_error", 0))
	sqlCursorStep(t, p, b, c, referenceExec("RELEASE cursor_error", 0))
}

func sqlCursorMissing(name string) referenceDiagnostic {
	return sqlCursorDiagnostic("34000", fmt.Sprintf("cursor %q does not exist", name))
}

func sqlCursorBatch(query string, rows [][]any, pg bool) referenceStep {
	return sqlCursorBatchMetadata(query, rows, pg, []bool{false, true})
}

func sqlCursorBatchMetadata(query string, rows [][]any, pg bool, nullable []bool) referenceStep {
	s := referenceSelect(query, []string{"i", "t"}, []string{"INT4", "TEXT"}, rows)
	s.checkMetadata = func(t *testing.T, r *sql.Rows) {
		driverBasicMetadata(t, r, pg, s.types, nullable)
	}
	return s
}

func sqlCursorOrdered(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	sqlCursorSetup(t, p, b, c)
	sqlCursorStep(t, p, b, c, referenceExec("BEGIN", 0))
	// Querying a utility command independently checks its empty result schema.
	sqlCursorStep(t, p, b, c, referenceSelect("DECLARE c NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", []string{}, []string{}, [][]any{}))
	query := "FETCH FORWARD 1 FROM c"
	stmt, err := c.PrepareContext(p.ctx, query)
	if err != nil {
		t.Fatal("quiet FETCH prepare", err)
	}
	held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: query}
	sqlCursorStep(t, p, b, held, sqlCursorBatch(query, [][]any{{int64(1), "one"}}, b.pg))
	sqlCursorStep(t, p, b, held, sqlCursorBatch(query, [][]any{{int64(2), nil}}, b.pg))
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH FORWARD ALL IN c", [][]any{{int64(3), "three"}}, b.pg))
	for range 2 {
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH ALL FROM c", [][]any{}, b.pg))
	}
	sqlCursorStep(t, p, b, c, referenceExec("CLOSE c", 0))
	sqlCursorError(t, p, b, c, "FETCH ALL FROM c", sqlCursorMissing("c"))
	sqlCursorStep(t, p, b, c, referenceExec("DECLARE c NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
	sqlCursorStep(t, p, b, c, referenceExec("COMMIT", 0))
	sqlCursorStep(t, p, b, c, referenceErrorDiagnostic("FETCH ALL FROM c", sqlCursorMissing("c")))
	sqlCursorStep(t, p, b, c, referenceExec("BEGIN", 0))
	sqlCursorStep(t, p, b, c, referenceExec("DECLARE c NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
	sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM c", [][]any{{int64(1), "one"}}, b.pg))
	sqlCursorStep(t, p, b, c, referenceExec("ROLLBACK", 0))
}

func TestReferenceSQLCursorOrderedBatches(t *testing.T) { sqlCursorScenario(t, sqlCursorOrdered) }

func TestReferenceSQLCursorFeatureSmoke(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		sqlCursorOrdered(t, p, b, c)
		sqlCursorDemand(t, p, b, c)
	})
}

func TestReferenceSQLCursorEmptyAndMetadata(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		sqlCursorStep(t, p, b, c, referenceExec("CREATE TABLE cursor_empty(i INTEGER NOT NULL,t TEXT)", 0))
		sqlCursorStep(t, p, b, c, referenceExec("BEGIN", 0))
		sqlCursorStep(t, p, b, c, referenceExec("DECLARE empty_table NO SCROLL CURSOR FOR SELECT i,t FROM cursor_empty", 0))
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH ALL FROM empty_table", [][]any{}, b.pg))
		sqlCursorStep(t, p, b, c, referenceExec("DECLARE zero_width NO SCROLL CURSOR FOR SELECT FROM cursor_rows", 0))
		sqlCursorStep(t, p, b, c, referenceSelect("FETCH 1 FROM zero_width", []string{}, []string{}, [][]any{{}}))
		sqlCursorStep(t, p, b, c, referenceExec("FETCH ALL FROM zero_width", 2))
		sqlCursorStep(t, p, b, c, referenceSelect("FETCH ALL FROM zero_width", []string{}, []string{}, [][]any{}))
		for _, predicate := range []string{"FALSE", "i > 99"} {
			sqlCursorStep(t, p, b, c, referenceExec("DECLARE e NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows WHERE "+predicate, 0))
			sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH ALL FROM e", [][]any{}, b.pg))
			sqlCursorStep(t, p, b, c, referenceExec("CLOSE e", 0))
		}
		sqlCursorStep(t, p, b, c, referencePrepared(referenceExec("DECLARE e NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows WHERE i = $1", 0, nil)))
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH ALL FROM e", [][]any{}, b.pg))
		sqlCursorStep(t, p, b, c, referenceExec("CLOSE e", 0))
		sqlCursorStep(t, p, b, c, referenceExec("DECLARE v NO SCROLL CURSOR FOR VALUES (1,'a'::TEXT),(2,NULL::TEXT)", 0))
		sqlCursorStep(t, p, b, c, referenceSelect("FETCH ALL FROM v", []string{"column1", "column2"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}}))
		sqlCursorStep(t, p, b, c, referenceExec("DECLARE aliases NO SCROLL CURSOR FOR SELECT i AS x,i AS x,t AS label FROM cursor_rows ORDER BY i", 0))
		sqlCursorStep(t, p, b, c, referenceSelect("FETCH ALL FROM aliases", []string{"x", "x", "label"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "one"}, {int64(2), int64(2), nil}, {int64(3), int64(3), "three"}}))
		sqlCursorStep(t, p, b, c, referenceExec("DECLARE oidtext NO SCROLL CURSOR FOR SELECT 'cursor_rows'::REGCLASS AS r", 0))
		sqlCursorStep(t, p, b, c, referenceSelect("FETCH ALL FROM oidtext", []string{"r"}, []string{"REGCLASS"}, [][]any{{"cursor_rows"}}))
		sqlCursorStep(t, p, b, c, referenceExec("CLOSE ALL", 0))
	})
}
