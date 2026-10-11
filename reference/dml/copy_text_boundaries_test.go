package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

type copyTextNoRead struct{ calls int }

func (r *copyTextNoRead) Read([]byte) (int, error) { r.calls++; return 0, io.EOF }
func TestReferenceCopyTextDatabaseSQLBoundary(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_boundary(v int)")
		if b.PG {
			result, err := c.from("COPY copy_boundary FROM STDIN", strings.NewReader("7\n"))
			copyTextSuccess(t, result, err, 1)
			c.output(t, "COPY copy_boundary TO STDOUT", "7\n", 1)
			reader := &copyTextNoRead{}
			result, err = stuntgres.CopyFrom(c.ctx, c.conn, "COPY copy_boundary FROM STDIN", reader)
			if err == nil || result != (copyTextResult{}) || reader.calls != 0 {
				t.Fatal("foreign oracle connection accepted native COPY", result, err, reader.calls)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("foreign adapter error acquired SQLSTATE", err)
			}
			return
		}
		for _, tc := range []struct{ q, message string }{
			{"COPY missing FROM STDIN", "COPY FROM STDIN requires stuntgres.CopyFrom"},
			{"COPY missing TO STDOUT", "COPY TO STDOUT requires stuntgres.CopyTo"},
			{"COPY missing FROM '/never-open-copy-text'", "COPY server files are unsupported"},
			{"COPY missing TO '/never-open-copy-text'", "COPY server files are unsupported"},
			{"COPY missing FROM PROGRAM 'never-execute-copy-text'", "COPY PROGRAM is unsupported"},
			{"COPY missing TO PROGRAM 'never-execute-copy-text'", "COPY PROGRAM is unsupported"},
		} {
			t.Run(tc.message+tc.q, func(t *testing.T) {
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: tc.message}
				for _, db := range []interface {
					ExecContext(context.Context, string, ...any) (sql.Result, error)
					QueryContext(context.Context, string, ...any) (*sql.Rows, error)
					PrepareContext(context.Context, string) (*sql.Stmt, error)
				}{b.DB, c.conn} {
					result, err := db.ExecContext(c.ctx, tc.q)
					if result != nil {
						t.Fatal("ordinary COPY exposed result")
					}
					fixture.AssertDiagnostic(t, err, &want)
					rows, err := db.QueryContext(c.ctx, tc.q)
					if rows != nil {
						rows.Close()
						t.Fatal("ordinary COPY exposed Rows")
					}
					fixture.AssertDiagnostic(t, err, &want)
					stmt, err := db.PrepareContext(c.ctx, tc.q)
					if stmt != nil {
						stmt.Close()
						t.Fatal("ordinary COPY exposed prepared handle")
					}
					fixture.AssertDiagnostic(t, err, &want)
				}
				if strings.Contains(tc.q, "PROGRAM") || strings.Contains(tc.q, "never-open") {
					reader := &copyTextNoRead{}
					result, err := c.from(tc.q, reader)
					copyTextDiagnostic(t, result, err, want)
					if reader.calls != 0 {
						t.Fatal("resource boundary read stream")
					}
				}
			})
		}
		for _, q := range []string{"", "SELECT 1", "COPY copy_boundary TO STDOUT", "COPY copy_boundary FROM STDIN; SELECT 1"} {
			reader := &copyTextNoRead{}
			result, err := c.from(q, reader)
			var state interface{ SQLState() string }
			if err == nil || errors.As(err, &state) || result != (copyTextResult{}) || reader.calls != 0 {
				t.Fatalf("argument boundary %q result=%+v err=%v calls=%d", q, result, err, reader.calls)
			}
		}
		for _, tc := range []struct {
			name string
			call func() (copyTextResult, error)
		}{
			{"nil_conn", func() (copyTextResult, error) {
				return stuntgres.CopyFrom(c.ctx, nil, "COPY copy_boundary FROM STDIN", strings.NewReader(""))
			}},
			{"nil_reader", func() (copyTextResult, error) {
				return stuntgres.CopyFrom(c.ctx, c.conn, "COPY copy_boundary FROM STDIN", nil)
			}},
			{"nil_writer", func() (copyTextResult, error) {
				return stuntgres.CopyTo(c.ctx, c.conn, "COPY copy_boundary TO STDOUT", nil)
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				result, err := tc.call()
				var state interface{ SQLState() string }
				if err == nil || errors.As(err, &state) || result != (copyTextResult{}) {
					t.Fatal(result, err)
				}
			})
		}
		wrapped, err := b.DB.Conn(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer wrapped.Close()
		result, err := stuntgres.CopyFrom(c.ctx, wrapped, "COPY copy_boundary FROM STDIN", strings.NewReader("7\n"))
		var state interface{ SQLState() string }
		if err == nil || errors.As(err, &state) || result != (copyTextResult{}) {
			t.Fatal("wrapped native connection accepted", result, err)
		}
		c.exec(t, "BEGIN")
		_, err = c.conn.ExecContext(c.ctx, "COPY copy_boundary FROM STDIN")
		fixture.State(t, err, "0A000")
		_, err = c.conn.ExecContext(c.ctx, "SELECT 1")
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		c.exec(t, "ROLLBACK")
	})
}
