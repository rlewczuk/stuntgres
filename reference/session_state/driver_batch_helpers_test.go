package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func batchScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			t.Cleanup(func() { _, _ = a.ExecContext(p.Ctx, "ROLLBACK") })
			body(t, p, b, a, peer)
		})
	}
}
func batchExec(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q string, n int64) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, n))
}
func batchValues(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q string, n ...int64) {
	t.Helper()
	rows := make([][]any, len(n))
	for i, v := range n {
		rows[i] = []any{v}
	}
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q, []string{"i"}, []string{"INT4"}, rows))
}
func batchDiag(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func batchPosition(q, token string) string {
	return fixture.Position(utf8.RuneCountInString(q[:strings.LastIndex(q, token)]) + 1)
}
func batchError(t *testing.T, p *fixture.Pair, x fixture.Executor, q string, w fixture.Diagnostic) {
	t.Helper()
	r, err := x.ExecContext(p.Ctx, q)
	if r != nil {
		t.Fatal("failed batch returned Result")
	}
	fixture.AssertDiagnostic(t, err, &w)
}

func batchQuery(t *testing.T, p *fixture.Pair, x fixture.Executor, q string) *sql.Rows {
	t.Helper()
	r, err := x.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}
func batchSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, a fixture.Executor) {
	t.Helper()
	batchExec(t, p, b, a, "CREATE TABLE batch_t(i INT NOT NULL)", 0)
	batchExec(t, p, b, a, "INSERT INTO batch_t VALUES(7)", 1)
}
