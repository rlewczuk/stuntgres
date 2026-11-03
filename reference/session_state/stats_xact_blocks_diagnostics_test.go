package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type xactBlockBadValuer struct{ err error }

func (v xactBlockBadValuer) Value() (driver.Value, error) { return nil, v.err }

func TestReferenceStatsXactBlocksDiagnostics(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, name := range xactBlockNames {
			for _, tc := range []struct{ suffix, signature string }{
				{"()", ""}, {"(NULL,NULL)", "unknown, unknown"}, {"(true)", "boolean"},
				{"('bad'::text)", "text"}, {"(x=>NULL)", "x => unknown"}, {"(*)", ""},
			} {
				want := activityDiagnostic("42883", "function "+name+"("+tc.signature+") does not exist", 8)
				want.Hint = hint
				activityError(t, p, c, "SELECT "+name+tc.suffix, want)
			}
			for _, tc := range []struct{ call, signature string }{
				{"public." + name + "(NULL)", "public." + name + "(unknown)"},
				{"\"" + strings.ToUpper(name) + "\"(NULL)", strings.ToUpper(name) + "(unknown)"},
				{name + "_missing(NULL)", name + "_missing(unknown)"},
			} {
				want := activityDiagnostic("42883", "function "+tc.signature+" does not exist", 8)
				want.Hint = hint
				activityError(t, p, c, "SELECT "+tc.call, want)
			}
			want := activityDiagnostic("42883", "function "+name+"(boolean) does not exist", 14)
			want.Hint = hint
			activityError(t, p, c, "/*é*/ SELECT "+name+"(true)", want)
			activityError(t, p, c, "SELECT xact_blocks_missing_schema."+name+"(NULL)", activityDiagnostic("3F000", `schema "xact_blocks_missing_schema" does not exist`, 8))
			for _, suffix := range []string{"(xact_blocks_missing_column)", "(true,xact_blocks_missing_column)"} {
				query := "SELECT " + name + suffix
				activityError(t, p, c, query, activityDiagnostic("42703", `column "xact_blocks_missing_column" does not exist`, strings.Index(query, "xact_blocks_missing_column")+1))
			}
			query := "SELECT " + name + "(1::xact_blocks_missing_type)"
			activityError(t, p, c, query, activityDiagnostic("42704", `type "xact_blocks_missing_type" does not exist`, strings.Index(query, "xact_blocks_missing_type")+1))
			for _, tc := range []struct{ suffix, message string }{
				{"(DISTINCT NULL::oid)", "DISTINCT specified, but " + name + " is not an aggregate function"},
				{"(NULL::oid ORDER BY 1)", "ORDER BY specified, but " + name + " is not an aggregate function"},
				{"(NULL::oid) FILTER (WHERE true)", "FILTER specified, but " + name + " is not an aggregate function"},
				{"(NULL::oid) OVER ()", "OVER specified, but " + name + " is not a window function nor an aggregate function"},
			} {
				activityError(t, p, c, "SELECT "+name+tc.suffix, activityDiagnostic("42809", tc.message, 8))
			}
			activityError(t, p, c, "SELECT "+name+"("+strings.Repeat("NULL,", 100)+"NULL)", activityDiagnostic("54023", "cannot pass more than 100 arguments to a function", 8))
			for _, tc := range []struct{ arguments, token, message string }{
				{"x=>NULL,x=>NULL", "x=>NULL)", `argument name "x" used more than once`},
				{"x=>NULL,NULL", "NULL)", "positional argument cannot follow named argument"},
			} {
				query := "SELECT " + name + "(" + tc.arguments + ")"
				activityError(t, p, c, query, activityDiagnostic("42601", tc.message, strings.Index(query, tc.token)+1))
			}
			for _, tc := range []struct{ value, code, message string }{
				{"bad", "22P02", `invalid input syntax for type oid: "bad"`},
				{"4294967296", "22003", `value "4294967296" is out of range for type oid`},
			} {
				activityError(t, p, c, "SELECT "+name+"('"+tc.value+"')", activityDiagnostic(tc.code, tc.message, 9+len(name)))
				for _, argument := range []string{"$1", "$1::oid"} {
					query := "SELECT " + name + "(" + argument + ")"
					for _, prepared := range []bool{false, true} {
						// Parameters fail at execution, not Prepare, and carry portal
						// context instead of a fabricated literal source position.
						want := activityDiagnostic(tc.code, tc.message, 0)
						want.Context = "unnamed portal parameter $1 = '...'"
						step := fixture.ErrorDiagnostic(query, want, tc.value)
						step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
					}
				}
			}
			query = "SELECT " + name + "($1::oid)"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New("xact blocks argument conversion")
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(p.Ctx, xactBlockBadValuer{failure})
				} else {
					rows, err = c.QueryContext(p.Ctx, query, xactBlockBadValuer{failure})
				}
				if rows != nil {
					rows.Close()
					t.Fatal("Valuer error published rows")
				}
				if !errors.Is(err, failure) {
					t.Fatal("ordinary Valuer error", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("Valuer error acquired SQLSTATE", err)
				}
				if prepared {
					rows, err = stmt.QueryContext(p.Ctx)
					if rows != nil {
						rows.Close()
						t.Fatal("argument count error published rows")
					}
					if err == nil || errors.As(err, &state) {
						t.Fatal("ordinary argument count error", err)
					}
				}
			}
			stmt.Close()
		}
		// Native pre-cancellation is an ordinary context error. pq can expose
		// Rows before reporting cancellation, or finish this fast scalar query.
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := c.QueryContext(ctx, xactBlockSQL, int64(0))
		count := 0
		if rows != nil {
			xactBlockMetadata(t, rows, xactBlockNames...)
			for rows.Next() {
				var fetched, hit int64
				if scanErr := rows.Scan(&fetched, &hit); scanErr != nil {
					rows.Close()
					t.Fatal("canceled getter scan", scanErr)
				}
				if fetched != 0 || hit != 0 {
					rows.Close()
					t.Fatal("fast getter result", fetched, hit)
				}
				count++
			}
			if rowErr := rows.Err(); err == nil {
				err = rowErr
			}
			if closeErr := rows.Close(); err == nil {
				err = closeErr
			}
		}
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled getter", err)
			}
		} else if !b.PG || count != 1 {
			t.Fatal("pre-canceled getter unexpectedly succeeded", b.Name, count)
		}
	})
}
