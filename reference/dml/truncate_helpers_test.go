package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func truncateScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, fixture.ControlsReferenceFixture)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b, fixture.NewControlsReferenceFixture(t, p, b.PG)) })
	}
}
func truncateWant(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func truncateFK(parent, child string) fixture.Diagnostic {
	d := truncateWant("0A000", "cannot truncate a table referenced in a foreign key constraint")
	d.Detail = `Table "` + child + `" references "` + parent + `".`
	d.Hint = `Truncate table "` + child + `" at the same time, or use TRUNCATE ... CASCADE.`
	return d
}
func truncateRun(t *testing.T, f fixture.ControlsReferenceFixture, x fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, f.Pair.Ctx, x, f.Recorder, s)
}
func truncateExec(t *testing.T, f fixture.ControlsReferenceFixture, x fixture.Executor, q string, count int64) {
	t.Helper()
	truncateRun(t, f, x, fixture.Exec(q, count))
}
func truncateRows(t *testing.T, f fixture.ControlsReferenceFixture, x fixture.Executor, table string, values ...int64) {
	t.Helper()
	rows := make([][]any, 0, len(values))
	for _, v := range values {
		rows = append(rows, []any{v})
	}
	truncateRun(t, f, x, fixture.Select("SELECT id FROM "+table+" ORDER BY id", []string{"id"}, []string{"INT4"}, rows))
}
func truncateError(t *testing.T, f fixture.ControlsReferenceFixture, x fixture.Executor, q string, d fixture.Diagnostic) {
	t.Helper()
	truncateRun(t, f, x, fixture.ErrorDiagnostic(q, d))
}
func truncateNotices(t *testing.T, f fixture.ControlsReferenceFixture, tables ...string) {
	t.Helper()
	want := make([]fixture.Diagnostic, 0, len(tables))
	for _, name := range tables {
		want = append(want, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `truncate cascades to table "` + name + `"`})
	}
	if len(*f.Notices) != len(want) || len(want) > 0 && !reflect.DeepEqual(*f.Notices, want) {
		t.Fatalf("notices got %#v want %#v", *f.Notices, want)
	}
	*f.Notices = nil
}
func truncateBegin(t *testing.T, p *fixture.Pair, c *sql.Conn, options *sql.TxOptions) *sql.Tx {
	t.Helper()
	tx, err := c.BeginTx(p.Ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	return tx
}
func truncateSeed(t *testing.T, f fixture.ControlsReferenceFixture) {
	t.Helper()
	truncateExec(t, f, f.A, "CREATE TABLE p(id INT PRIMARY KEY)", 0)
	truncateExec(t, f, f.A, "CREATE TABLE q(id INT PRIMARY KEY)", 0)
	truncateExec(t, f, f.A, "INSERT INTO p VALUES(1),(2)", 2)
	truncateExec(t, f, f.A, "INSERT INTO q VALUES(7)", 1)
}
