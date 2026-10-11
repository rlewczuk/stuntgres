package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"stuntgres"
	"stuntgres/reference/fixture"
)

type copyTextResult = stuntgres.CopyResult
type copyTextConnection struct {
	conn *sql.Conn
	pg   bool
	ctx  context.Context
}

func copyTextScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, copyTextConnection)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			db := p.Native
			if b.PG {
				db = p.CopyPostgres
			}
			conn, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			body(t, p, b, copyTextConnection{conn: conn, pg: b.PG, ctx: p.Ctx})
		})
	}
}

func (c copyTextConnection) from(q string, r io.Reader) (copyTextResult, error) {
	if !c.pg {
		return stuntgres.CopyFrom(c.ctx, c.conn, q, r)
	}
	var result copyTextResult
	err := c.conn.Raw(func(raw any) error {
		conn, ok := raw.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("unexpected oracle COPY connection %T", raw)
		}
		tag, err := conn.Conn().PgConn().CopyFrom(c.ctx, r, q)
		if err == nil {
			result = copyTextResult{tag.RowsAffected(), tag.String()}
		}
		return err
	})
	return result, err
}

func (c copyTextConnection) to(q string, w io.Writer) (copyTextResult, error) {
	if !c.pg {
		return stuntgres.CopyTo(c.ctx, c.conn, q, w)
	}
	var result copyTextResult
	err := c.conn.Raw(func(raw any) error {
		conn, ok := raw.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("unexpected oracle COPY connection %T", raw)
		}
		tag, err := conn.Conn().PgConn().CopyTo(c.ctx, w, q)
		if err == nil {
			result = copyTextResult{tag.RowsAffected(), tag.String()}
		}
		return err
	})
	return result, err
}

func (c copyTextConnection) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := c.conn.ExecContext(c.ctx, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}
func copyTextSuccess(t *testing.T, got copyTextResult, err error, count int64) {
	t.Helper()
	want := copyTextResult{count, fmt.Sprintf("COPY %d", count)}
	if err != nil || got != want {
		t.Fatalf("COPY result=%+v err=%v want=%+v", got, err, want)
	}
}
func (c copyTextConnection) output(t *testing.T, q, want string, count int64) {
	t.Helper()
	var buf bytes.Buffer
	result, err := c.to(q, &buf)
	copyTextSuccess(t, result, err, count)
	if buf.String() != want {
		t.Fatalf("%s bytes=%q want=%q", q, buf.String(), want)
	}
}
func (c copyTextConnection) selectRows(t *testing.T, q string, names, types []string, want [][]any) {
	t.Helper()
	rows, err := c.conn.QueryContext(c.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	gotNames, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotNames, names) {
		t.Fatalf("names=%v want=%v", gotNames, names)
	}
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, col := range columns {
		if col.DatabaseTypeName() != types[i] {
			t.Fatalf("column %d type=%s want=%s", i, col.DatabaseTypeName(), types[i])
		}
	}
	got := make([][]any, 0)
	for rows.Next() {
		values := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range ptrs {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s rows=%#v want=%#v", q, got, want)
	}
}
func copyTextDiagnostic(t *testing.T, result copyTextResult, err error, want fixture.Diagnostic) {
	t.Helper()
	if result != (copyTextResult{}) {
		t.Fatalf("failed COPY returned success result %+v", result)
	}
	if want.Severity == "" {
		want.Severity = "ERROR"
	}
	if want.SeverityUnlocalized == "" {
		want.SeverityUnlocalized = want.Severity
	}
	fixture.AssertDiagnostic(t, err, &want)
}
