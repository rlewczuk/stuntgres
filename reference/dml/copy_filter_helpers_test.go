package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Backend is the immediate child, allowing oracle-first discovery with the
// actual /postgres subtest selector, without a temporary native skip.
func copyFilterScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, copyTextConnection, *[]fixture.Diagnostic)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			notices := make([]fixture.Diagnostic, 0)
			conn := fixture.CopyNoticeConnection(t, p, b.PG, &notices, nil)
			body(t, p, b, copyTextConnection{conn: conn, pg: b.PG, ctx: p.Ctx}, &notices)
		})
	}
}

func copyFilterImport(t *testing.T, c copyTextConnection, q, input string, count int64) {
	t.Helper()
	r, err := c.from(q, strings.NewReader(input))
	copyTextSuccess(t, r, err, count)
}

func copyFilterFailure(t *testing.T, c copyTextConnection, q, input string, want fixture.Diagnostic) {
	t.Helper()
	r, err := c.from(q, strings.NewReader(input))
	copyTextDiagnostic(t, r, err, want)
}

func copyFilterNotices(t *testing.T, records []fixture.Diagnostic, want ...fixture.Diagnostic) {
	t.Helper()
	if len(records) != len(want) {
		t.Fatalf("notices=%#v want=%#v", records, want)
	}
	for i := range want {
		if records[i] != want[i] {
			t.Errorf("notice[%d]=%#v want=%#v", i, records[i], want[i])
		}
	}
}

func copyFilterSummary(n int) fixture.Diagnostic {
	message := fmt.Sprintf("%d rows were skipped due to data type incompatibility", n)
	if n == 1 {
		message = "1 row was skipped due to data type incompatibility"
	}
	return fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message}
}

func copyFilterRowNotice(table, column, value string, line int) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000",
		Message: fmt.Sprintf("skipping row due to data type incompatibility at line %d for column %q: %s", line, column, value), Context: "COPY " + table}
}

func copyFilterIntError(table, value string, line int) fixture.Diagnostic {
	return fixture.Diagnostic{Code: "22P02", Message: fmt.Sprintf("invalid input syntax for type integer: %q", value),
		Context: fmt.Sprintf("COPY %s, line %d, column id: %q", table, line, value)}
}

func copyFilterLimitError(table, value string, line, limit int) fixture.Diagnostic {
	d := copyFilterIntError(table, value, line)
	d.Message = fmt.Sprintf("skipped more than REJECT_LIMIT (%d) rows due to data type incompatibility", limit)
	return d
}

func copyFilterPosition(q, token string) string {
	i := strings.LastIndex(q, token)
	if i < 0 {
		panic("diagnostic token absent: " + token)
	}
	return fixture.Position(utf8.RuneCountInString(q[:i]) + 1)
}

func copyFilterIDs(t *testing.T, c copyTextConnection, table string, ids ...int64) {
	t.Helper()
	rows := make([][]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	c.selectRows(t, "SELECT id FROM "+table+" ORDER BY id", []string{"id"}, []string{"INT4"}, rows)
}

// The direct and held statement execute on wrapped owners, not the Raw COPY
// connection. Retaining this actual statement across COPY exercises reuse and
// the fixture's owned middleware row buffers.
func copyFilterHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, q string) (*sql.Conn, *sql.Stmt) {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stmt.Close() })
	return conn, stmt
}

func copyFilterConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, stmt *sql.Stmt, step fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, step)
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: step.Query}, b.Recorder, step)
}

func copyFilterEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%#v want=%#v", got, want)
	}
}
