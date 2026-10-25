package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations come from PostgreSQL 18 F.16.3 and REL_18_0, not native output.
func levenshteinCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"lev_reader", "lev_owner"})
	fixture.SchemaReferenceCleanup(t, "lev_addon")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if backend.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP EXTENSION IF EXISTS fuzzystrmatch CASCADE", "DROP SCHEMA IF EXISTS lev_addon CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			body(t, p, backend, c)
		})
	}
}

// levenshteinOutOfRange is PostgreSQL 18.6's error for a distance outside int4.
var levenshteinOutOfRange = fixture.Diagnostic{Code: "22003", Message: "levenshtein distance out of range"}

func levenshteinSelect(q, name string, value any, args ...any) fixture.Step {
	if d, ok := value.(fixture.Diagnostic); ok {
		s := fixture.ImplicitError(q, d, args...)
		s.SelectRows, s.ErrorStage = true, "execute"
		return s
	}
	s := fixture.Select(q, []string{name}, []string{"INT4"}, [][]any{{value}}, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
			t.Fatal("INT4 metadata", types, err)
		}
	}
	return s
}

func levenshteinSQL(name string, literal bool, args []any) string {
	parts := make([]string, len(args))
	for i, arg := range args {
		if !literal {
			parts[i] = fmt.Sprintf("$%d", i+1)
			continue
		}
		switch v := arg.(type) {
		case nil:
			parts[i] = "NULL"
		case string:
			parts[i] = "'" + strings.ReplaceAll(v, "'", "''") + "'"
		default:
			parts[i] = fmt.Sprint(v)
			if parts[i] == "-2147483648" {
				parts[i] = "'-2147483648'::int"
			}
		}
	}
	return "SELECT " + name + "(" + strings.Join(parts, ",") + ")"
}

func levenshteinFrames(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, frames []struct {
	args []any
	want any
}) {
	t.Helper()
	q := levenshteinSQL(name, false, frames[0].args)
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for i, frame := range frames {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s := levenshteinSelect(q, name, frame.want, frame.args...)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, levenshteinSelect(levenshteinSQL(name, true, frame.args), name, frame.want))
		})
	}
}

func levenshteinLengthError(q string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: "levenshtein argument exceeds maximum length of 255 characters"}, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

// Out-of-bound distances have no exact sentinel guarantee. Inspect the actual
// INT4 result, then record an independently specified SQL predicate companion.
func levenshteinOutside(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, args []any, bound int64) {
	t.Helper()
	q := levenshteinSQL("levenshtein_less_equal", false, args)
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		var err error
		if prepared {
			stmt, e := c.PrepareContext(p.Ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			rows, err = stmt.QueryContext(p.Ctx, args...)
		} else {
			rows, err = c.QueryContext(p.Ctx, q, args...)
		}
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer rows.Close()
			levenshteinSelect(q, "levenshtein_less_equal", nil).CheckMetadata(t, rows)
			columns, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(columns, []string{"levenshtein_less_equal"}) {
				t.Fatal(columns, err)
			}
			if !rows.Next() {
				t.Fatal("missing bounded row", rows.Err())
			}
			var raw any
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			value, ok := raw.(int64)
			if !ok || value <= bound {
				t.Fatalf("bounded result %#v must be INT4 > %d", raw, bound)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("bounded EOF", rows.Err())
			}
		}()
	}
	predicate := q + fmt.Sprintf(" > %d AS outside", bound)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(predicate, []string{"outside"}, []string{"BOOL"}, [][]any{{true}}, args...))
}
