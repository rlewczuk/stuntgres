package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func colcuWriteDiagnostic(operation string) fixture.Diagnostic {
	verb, action := "insert into", "inserting into"
	if operation == "UPDATE" {
		verb, action = "update", "updating"
	}
	if operation == "DELETE" {
		verb, action = "delete from", "deleting from"
	}
	d := fixture.ControlsReferenceDiagnostic("55000", `cannot `+verb+` view "column_column_usage"`)
	d.Detail = "Views containing DISTINCT are not automatically updatable."
	d.Hint = "To enable " + action + " the view, provide an INSTEAD OF " + operation + " trigger or an unconditional ON " + operation + " DO INSTEAD rule."
	return d
}

func TestReferenceInfoColumnColumnUsageDiagnostics(t *testing.T) {
	colcuScenario(t, func(c *colcuCase) {
		c.exec(colcuSeed, "CREATE TABLE colcu_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO colcu_sentinel VALUES(91)", 1))
		held := c.hold(c.conn, colcuQuery)
		unchanged := func() {
			c.check(held, "colcu_usage", c.seedRows()...)
			c.run(c.conn, fixture.Select("SELECT v FROM colcu_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		}
		for _, tc := range []struct{ operation, query string }{
			{"INSERT", "INSERT INTO information_schema.column_column_usage(table_catalog) VALUES('x')"},
			{"UPDATE", "UPDATE information_schema.column_column_usage SET table_catalog='x'"},
			{"UPDATE", "UPDATE information_schema.column_column_usage SET table_catalog='x' WHERE false"},
			{"DELETE", "DELETE FROM information_schema.column_column_usage"},
			{"DELETE", "DELETE FROM information_schema.column_column_usage WHERE false"},
		} {
			c.t.Run(tc.query, func(t *testing.T) {
				child := *c
				child.t = t
				d := colcuWriteDiagnostic(tc.operation)
				// PostgreSQL rewrites this nonupdatable view during protocol Prepare.
				// Repeat both routes: no executable held write statement is published.
				for range 2 {
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(tc.query, d)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						child.run(c.conn, s)
					}
				}
			})
			unchanged()
		}
		for _, tc := range []struct {
			query, message, token string
			selectRows            bool
		}{
			{"SELECT missing FROM information_schema.column_column_usage", `column "missing" does not exist`, "missing", true},
			{"/* éλ */ SELECT missing FROM information_schema.column_column_usage", `column "missing" does not exist`, "missing", true},
			{"UPDATE information_schema.column_column_usage SET missing='x' WHERE false", `column "missing" of relation "column_column_usage" does not exist`, "missing", false},
			{"INSERT INTO information_schema.column_column_usage(missing) VALUES('x')", `column "missing" of relation "column_column_usage" does not exist`, "missing", false},
		} {
			c.t.Run(tc.query, func(t *testing.T) {
				child := *c
				child.t = t
				d := fixture.ControlsReferenceDiagnostic("42703", tc.message)
				d.Position = fixture.Position(utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.token)]) + 1)
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.query, d)
					s.SelectRows = tc.selectRows
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					child.run(c.conn, s)
				}
			})
			unchanged()
		}
		for _, tc := range []struct{ expr, typ, code, message, detail, token string }{
			{"b+1", "integer", "42P17", `cannot use generated column "b" in column generation expression`, "A generated column cannot reference another generated column.", "b+1"},
			{"random()", "double precision", "42P17", "generation expression is not immutable", "", ""},
			{"missing+1", "integer", "42703", `column "missing" does not exist`, "", "missing+1"},
		} {
			q := "/*é*/ CREATE TABLE colcu_failed(a integer,b " + tc.typ + " GENERATED ALWAYS AS(" + tc.expr + ") STORED)"
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Detail = tc.detail
			if tc.token != "" {
				d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)
			}
			c.t.Run(tc.expr, func(t *testing.T) {
				child := *c
				child.t = t
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, d)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					child.run(c.conn, s)
				}
			})
			unchanged()
			c.check(c.conn, "colcu_failed")
			c.run(c.conn, fixture.Select("SELECT relname FROM pg_class WHERE relname='colcu_failed'", []string{"relname"}, []string{"NAME"}, nil))
			c.run(c.conn, fixture.Select("SELECT a.adnum FROM pg_attrdef a JOIN pg_class r ON r.oid=a.adrelid WHERE r.relname='colcu_failed'", []string{"adnum"}, []string{"INT2"}, nil))
		}
		c.exec("CREATE TABLE colcu_add(a integer)")
		q := "ALTER TABLE colcu_add ADD COLUMN b integer GENERATED ALWAYS AS(a+1) STORED"
		if c.b.PG {
			c.exec(q)
			c.check(c.conn, "colcu_add", c.pub("colcu_add", "a", "b"))
		} else {
			d := fixture.ControlsReferenceDiagnostic("0A000", "ADD COLUMN generated declarations are unsupported")
			d.Position = fixture.GeneratedStoredPosition(q, "GENERATED")
			c.run(c.conn, fixture.ErrorDiagnostic(q, d))
			c.check(c.conn, "colcu_add")
		}
		if !c.b.PG {
			ctx, cancel := context.WithCancel(c.p.Ctx)
			cancel()
			for _, e := range []fixture.Executor{c.conn, held} {
				rows, err := e.QueryContext(ctx, colcuQuery, "colcu_usage")
				if rows != nil {
					rows.Close()
					c.t.Fatal("canceled read exposed rows")
				}
				if !errors.Is(err, context.Canceled) {
					c.t.Fatal("cancellation", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					c.t.Fatal("ordinary cancellation has SQLSTATE", state.SQLState())
				}
			}
		} else {
			fixture.SessionCancel(c.t, c.p.Ctx, c.conn, false, true)
		}
		unchanged()
	})
}
