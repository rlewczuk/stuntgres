package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayTransformAuthorityControls(t *testing.T) {
	t.Run("column_authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"at_reader"})
		arrayTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Error(err)
				}
			}()
			for _, q := range []string{
				"CREATE ROLE at_reader", "CREATE TABLE at_acl(id INT,a INT[],x INT,dims INT[],bounds INT[],flag BOOL,delim TEXT,marker TEXT)",
				"INSERT INTO at_acl VALUES(1,NULL,NULL,NULL,NULL,NULL,NULL,NULL)",
				"GRANT SELECT(id) ON at_acl TO at_reader",
			} {
				count := int64(0)
				if q == "INSERT INTO at_acl VALUES(1,NULL,NULL,NULL,NULL,NULL,NULL,NULL)" {
					count = 1
				}
				run(fixture.Exec(q, count))
			}
			if b.PG {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO at_reader", 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION at_reader", 0))
			for _, expr := range []string{
				"array_append(NULL::int[],x)", "array_prepend(x,NULL::int[])", "array_cat(NULL::int[],a)",
				"array_fill(1,dims)", "array_fill(1,ARRAY[1],bounds)", "array_reverse(a)",
				"array_sort(NULL::int[],flag)", "array_sort(NULL::int[],false,flag)",
				"trim_array(NULL::int[],x)", "array_to_string(NULL::int[],delim)",
				"array_to_string(NULL::int[],',',marker)", "NULL::int[] || x",
			} {
				for _, suffix := range []string{"", " WHERE FALSE", " LIMIT 0"} {
					t.Run(expr+suffix, func(t *testing.T) {
						arrayTransformDiagnostic(t, p, b, c, "SELECT "+expr+" AS a FROM at_acl"+suffix, fixture.Diagnostic{Code: "42501", Message: "permission denied for table at_acl"}, false)
					})
				}
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT SELECT(a,x,dims,bounds,flag,delim,marker) ON at_acl TO at_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION at_reader", 0))
			const q = "SELECT array_sort(a,flag) AS sorted,array_to_string(a,delim,marker) AS joined FROM at_acl"
			check := arrayTransformSelect(b, q, []string{"sorted", "joined"}, []string{"_INT4", "TEXT"}, [][]any{{nil, nil}})
			arrayTransformBoth(t, p, b, c, check)
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("REVOKE SELECT(marker) ON at_acl FROM at_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION at_reader", 0))
			bad := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table at_acl"})
			bad.SelectRows, bad.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT SELECT(marker) ON at_acl TO at_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION at_reader", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	})
	t.Run("late_write_atomicity", func(t *testing.T) {
		arrayTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE at_atomic(id INT,a INT[])", 0))
			run(fixture.Exec("INSERT INTO at_atomic VALUES(0,'{42}')", 1))
			run(fixture.Exec("CREATE TABLE at_source(id INT)", 0))
			run(fixture.Exec("INSERT INTO at_source VALUES(1),(2)", 2))
			unchanged := arrayTransformSelect(b, "SELECT id,a FROM at_atomic ORDER BY id", []string{"id", "a"}, []string{"INT4", "_INT4"}, [][]any{{int64(0), "{42}"}})
			for _, tc := range []struct {
				name, left, right, code, message, detail string
				args                                     []any
			}{
				{"cat", "array_cat($1::int[],$2::int[])", "array_cat($1::int[],$3::int[])", "2202E", "cannot concatenate incompatible arrays", "Arrays of 1 and 3 dimensions are not compatible for concatenation.", []any{"{1}", "{2}", "{{{3}}}"}},
				{"fill", "array_fill(7,$1::int[])", "array_fill(7,$2::int[])", "22004", "dimension values cannot be null", "", []any{"{1}", "{NULL}"}},
				{"trim", "trim_array($1::int[],0)", "trim_array($1::int[],2)", "2202E", "number of elements to trim must be between 0 and 1", "", []any{"{1}"}},
			} {
				for _, q := range []string{
					"INSERT INTO at_atomic VALUES(1," + tc.left + "),(2," + tc.right + ")",
					"INSERT INTO at_atomic SELECT id,CASE WHEN id=1 THEN " + tc.left + " ELSE " + tc.right + " END FROM at_source ORDER BY id",
				} {
					t.Run(tc.name+q, func(t *testing.T) {
						bad := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail}, tc.args...)
						bad.ErrorStage = "execute"
						for _, prepared := range []bool{false, true} {
							bad.Prepared = prepared
							run(bad)
							run(unchanged)
						}
					})
				}
			}
			// Native context cancellation is an adapter control, not PG transport parity.
			if b.PG {
				return
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := c.QueryContext(ctx, "SELECT array_reverse(ARRAY[1]) AS a FROM at_one")
			if rows != nil {
				rows.Close()
				t.Fatal("precancel published cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("precancel=%v", err)
			}
			const active = "SELECT array_to_string(array_fill(123456789,ARRAY[1000000]),',') AS a FROM at_one"
			fixture.BinaryConversionActiveCancel(t, p, b, c, active, "ArrayTransform", func(ctx context.Context) fixture.RelationOutcome {
				r, err := c.QueryContext(ctx, active)
				return fixture.RelationOutcome{Rows: r, Err: err}
			})
			run(unchanged)
			arrayTransformBoth(t, p, b, c, arrayTransformSelect(b, "SELECT array_reverse(ARRAY[1,2]) AS a FROM at_one", []string{"a"}, []string{"_INT4"}, [][]any{{"{2,1}"}}))
		})
	})
}
