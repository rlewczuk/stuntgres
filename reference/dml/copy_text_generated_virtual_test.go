package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualCopyConstraints(t *testing.T) {
	for _, tc := range []struct {
		name, recipe, input string
		rows                [][]any
		want                *fixture.Diagnostic
	}{
		{name: "non_null", recipe: "a+1", input: "2\n", rows: [][]any{{int64(2), int64(3)}}},
		{name: "constant_null_owner", recipe: "9", input: "\\N\n", rows: [][]any{{nil, int64(9)}}},
		{name: "non_strict_null_owner", recipe: "COALESCE(a,9)", input: "\\N\n", rows: [][]any{{nil, int64(9)}}},
		{name: "logical_null", recipe: "a+1", input: "\\N\n", rows: [][]any{}, want: &fixture.Diagnostic{Code: "23502", Message: `null value in column "b" of relation "virtual_copy_nn" violates not-null constraint`, Detail: "Failing row contains (null, virtual).", Schema: "public", Table: "virtual_copy_nn", Column: "b", Context: `COPY virtual_copy_nn, line 1: "\N"`}},
		{name: "generation_error", recipe: "1/a", input: "0\n", rows: [][]any{}, want: &fixture.Diagnostic{Code: "22012", Message: "division by zero", Context: `COPY virtual_copy_nn, line 1: "0"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
				c.exec(t, `CREATE TABLE virtual_copy_nn(a INT,b INT GENERATED ALWAYS AS(`+tc.recipe+`) VIRTUAL NOT NULL)`)
				result, err := c.from(`COPY virtual_copy_nn(a) FROM STDIN`, strings.NewReader(tc.input))
				if tc.want == nil {
					copyTextSuccess(t, result, err, 1)
				} else {
					copyTextDiagnostic(t, result, err, *tc.want)
				}
				c.selectRows(t, `SELECT a,b FROM virtual_copy_nn`, []string{"a", "b"}, []string{"INT4", "INT4"}, tc.rows)
			})
		})
	}
	t.Run("nullable_remains_lazy", func(t *testing.T) {
		copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
			c.exec(t, `CREATE TABLE virtual_copy_lazy(a INT,b INT GENERATED ALWAYS AS(1/a) VIRTUAL)`)
			result, err := c.from(`COPY virtual_copy_lazy(a) FROM STDIN`, strings.NewReader("0\n"))
			copyTextSuccess(t, result, err, 1)
			c.selectRows(t, `SELECT a FROM virtual_copy_lazy`, []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}})
			fixture.RunStep(t, c.ctx, c.conn, b.Recorder, fixture.ImplicitError(`SELECT b FROM virtual_copy_lazy`, fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
			c.selectRows(t, `SELECT a FROM virtual_copy_lazy`, []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}})
		})
	})
}

func TestReferenceGeneratedVirtualCopyAtomicity(t *testing.T) {
	for _, tc := range []struct {
		name, recipe, input string
		rows                [][]any
		copied              int64
		want                fixture.Diagnostic
	}{
		{name: "late_logical_null", recipe: "a+1", input: "4\n\\N\n", rows: [][]any{{int64(2), int64(3)}, {int64(3), int64(4)}}, copied: 5, want: fixture.Diagnostic{Code: "23502", Message: `null value in column "b" of relation "virtual_copy_atomic" violates not-null constraint`, Detail: "Failing row contains (null, virtual).", Schema: "public", Table: "virtual_copy_atomic", Column: "b", Context: `COPY virtual_copy_atomic, line 2: "\N"`}},
		{name: "late_generation_error", recipe: "1/a", input: "4\n0\n", rows: [][]any{{int64(2), int64(0)}, {int64(3), int64(0)}}, copied: 0, want: fixture.Diagnostic{Code: "22012", Message: "division by zero", Context: `COPY virtual_copy_atomic, line 2: "0"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
				c.exec(t, `CREATE TABLE virtual_copy_atomic(a INT,b INT GENERATED ALWAYS AS(`+tc.recipe+`) VIRTUAL NOT NULL)`)
				c.exec(t, `INSERT INTO virtual_copy_atomic(a) VALUES(2),(3)`)
				result, err := c.from(`COPY virtual_copy_atomic(a) FROM STDIN`, strings.NewReader(tc.input))
				copyTextDiagnostic(t, result, err, tc.want)
				c.selectRows(t, `SELECT a,b FROM virtual_copy_atomic ORDER BY a`, []string{"a", "b"}, []string{"INT4", "INT4"}, tc.rows)
				c.exec(t, `BEGIN`)
				result, err = c.from(`COPY virtual_copy_atomic(a) FROM STDIN`, strings.NewReader("4\n"))
				copyTextSuccess(t, result, err, 1)
				c.selectRows(t, `SELECT a,b FROM virtual_copy_atomic WHERE a=4`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(4), tc.copied}})
				c.exec(t, `ROLLBACK`)
				c.selectRows(t, `SELECT a,b FROM virtual_copy_atomic ORDER BY a`, []string{"a", "b"}, []string{"INT4", "INT4"}, tc.rows)
				c.exec(t, `BEGIN`)
				c.exec(t, `SAVEPOINT virtual_copy_mark`)
				result, err = c.from(`COPY virtual_copy_atomic(a) FROM STDIN`, strings.NewReader(tc.input))
				copyTextDiagnostic(t, result, err, tc.want)
				_, err = c.conn.ExecContext(c.ctx, `SELECT a FROM virtual_copy_atomic`)
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
				c.exec(t, `ROLLBACK TO virtual_copy_mark`)
				c.selectRows(t, `SELECT a,b FROM virtual_copy_atomic ORDER BY a`, []string{"a", "b"}, []string{"INT4", "INT4"}, tc.rows)
				c.exec(t, `ROLLBACK`)
				c.selectRows(t, `SELECT a,b FROM virtual_copy_atomic ORDER BY a`, []string{"a", "b"}, []string{"INT4", "INT4"}, tc.rows)
			})
		})
	}
}
