package reference_test

import (
	"database/sql"
	"slices"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLCursorParameters(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		query := "DECLARE param NO SCROLL CURSOR FOR SELECT i,$1::TEXT AS marker FROM cursor_rows WHERE i >= $2 ORDER BY i"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal("DECLARE prepare", err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		// Preparing outside a transaction must not install a portal.
		sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM param", sqlCursorMissing("param")))
		sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
		marker := []byte("before")
		sqlCursorStep(t, p, b, held, fixture.Exec(query, 0, marker, 2))
		copy(marker, "after!")
		sqlCursorError(t, p, b, held, query, sqlCursorDiagnostic("42P03", "cursor \"param\" already exists"), "duplicate", 2)
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM param", []string{"i", "marker"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "before"}, {int64(3), "before"}}))
		sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE param", 0))
		fresh, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		held = fixture.HeldStatement{Executor: c, Statement: fresh, SQL: query}
		sqlCursorStep(t, p, b, held, fixture.Exec(query, 0, "fresh", 3))
		if err := fresh.Close(); err != nil {
			t.Fatal(err)
		}
		sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM param", []string{"i", "marker"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "fresh"}}))
	})
}

func TestReferenceSQLCursorPreparedUtilities(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		declare := "DECLARE c NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i"
		d, err := c.PrepareContext(p.Ctx, declare)
		if err != nil {
			t.Fatal("outside-block DECLARE prepare", err)
		}
		defer d.Close()
		sqlCursorStep(t, p, b, fixture.HeldStatement{Executor: c, Statement: d, SQL: declare}, fixture.ErrorDiagnostic(declare, sqlCursorDiagnostic("25P01", "DECLARE CURSOR can only be used in transaction blocks")))
		for _, query := range []string{"FETCH ALL FROM absent", "CLOSE absent"} {
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal("absent utility must prepare quietly", query, err)
			}
			sqlCursorStep(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, fixture.ErrorDiagnostic(query, sqlCursorMissing("absent")))
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
		sqlCursorStep(t, p, b, fixture.HeldStatement{Executor: c, Statement: d, SQL: declare}, fixture.Exec(declare, 0))
		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
		closeAll, err := c.PrepareContext(p.Ctx, "CLOSE ALL")
		if err != nil {
			t.Fatal(err)
		}
		defer closeAll.Close()
		query := "FETCH FORWARD 1 FROM c"
		fetch, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer fetch.Close()
		held := fixture.HeldStatement{Executor: c, Statement: fetch, SQL: query}
		sqlCursorStep(t, p, b, held, sqlCursorBatch(query, [][]any{{int64(1), "one"}}, b.PG))
		sqlCursorStep(t, p, b, held, sqlCursorBatch(query, [][]any{{int64(2), nil}}, b.PG))
		sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE c", 0))
		sqlCursorError(t, p, b, held, query, sqlCursorMissing("c"))
	})
}

func TestReferenceSQLCursorPreparedMetadata(t *testing.T) {
	for _, name := range []string{"absent_then_declared", "same_width", "rebound"} {
		absent, sameWidth := name == "absent_then_declared", name == "same_width"
		t.Run(name, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
				if !absent {
					sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE rebound NO SCROLL CURSOR FOR SELECT 1 AS old", 0))
				}
				stmt, err := c.PrepareContext(p.Ctx, "FETCH ALL FROM rebound")
				if err != nil {
					t.Fatal("FETCH prepare phase", err)
				}
				defer stmt.Close()
				if !absent {
					sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE rebound", 0))
				}
				declaration := "DECLARE rebound NO SCROLL CURSOR FOR SELECT 'new'::TEXT AS fresh,2 AS number"
				columns, types, values := []string{"fresh", "number"}, []string{"TEXT", "INT4"}, [][]any{{"new", int64(2)}}
				if sameWidth {
					declaration = "DECLARE rebound NO SCROLL CURSOR FOR SELECT 2 AS fresh"
					columns, types, values = []string{"fresh"}, []string{"INT4"}, [][]any{{int64(2)}}
				}
				sqlCursorStep(t, p, b, c, fixture.Exec(declaration, 0))
				query := "FETCH ALL FROM rebound"
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				if !b.PG {
					// Native public results describe the live runtime portal, not
					// lib/pq's retained wire description/binary decoder.
					sqlCursorStep(t, p, b, held, fixture.Select(query, columns, types, values))
				} else if absent {
					// The quiet absent Prepare advertises zero columns even after
					// declaration. Its execution still returns one occurrence.
					sqlCursorStep(t, p, b, held, fixture.Select(query, []string{}, []string{}, [][]any{{}}))
				} else if sameWidth {
					// Safe INT4→INT4 rebind: actual held execution returns the new
					// value but retains the old advertised column alias.
					sqlCursorStep(t, p, b, held, fixture.Select(query, []string{"old"}, []string{"INT4"}, [][]any{{int64(2)}}))
				} else {
					// pq retains the old INT4 binary decoder as well as metadata.
					// Reading TEXT 'new' through it panics (tests-1.md). Inspect
					// the advertised description without Next; do not recover or
					// suppress the transport failure, or claim decoded row parity.
					rows, err := stmt.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal("rebound Query phase", err)
					}
					defer rows.Close()
					columns, err := rows.Columns()
					if err != nil {
						t.Fatal(err)
					}
					types, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					if !slices.Equal(columns, []string{"old"}) || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
						t.Fatalf("stale FETCH description columns=%#v types=%#v", columns, types)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
				}
				// Execution consumed the runtime portal despite an absent/stale
				// description. Fresh direct FETCH independently observes its schema.
				sqlCursorStep(t, p, b, c, fixture.Select(query, columns, types, [][]any{}))
				sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE rebound", 0))
				sqlCursorStep(t, p, b, c, fixture.Exec(declaration, 0))
				sqlCursorStep(t, p, b, c, fixture.Select(query, columns, types, values))
			})
		})
	}
}

func TestReferenceSQLCursorAnalysisAndDiagnostics(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("DECLARE c NO SCROLL CURSOR FOR SELECT i FROM cursor_rows", sqlCursorDiagnostic("25P01", "DECLARE CURSOR can only be used in transaction blocks")))
		sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
		sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE \"Mixed Case\" NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
		sqlCursorError(t, p, b, c, "DECLARE \"Mixed Case\" NO SCROLL CURSOR FOR SELECT i FROM cursor_rows", sqlCursorDiagnostic("42P03", "cursor \"Mixed Case\" already exists"))
		sqlCursorError(t, p, b, c, "FETCH ALL FROM \"mixed case\"", sqlCursorMissing("mixed case"))
		sqlCursorError(t, p, b, c, "CLOSE \"missing case\"", sqlCursorMissing("missing case"))
		for _, probe := range []struct{ query, code, message, position string }{
			{"DECLARE broken NO SCROLL CURSOR FOR SELECT i FROM cursor_missing", "42P01", "relation \"cursor_missing\" does not exist", "51"},
			{"DECLARE broken NO SCROLL CURSOR FOR SELECT absent FROM cursor_rows", "42703", "column \"absent\" does not exist", "44"},
			{"DECLARE broken NO SCROLL CURSOR FOR SELECT cursor_unknown(i) FROM cursor_rows", "42883", "function cursor_unknown(integer) does not exist", "44"},
			{"DECLARE broken NO SCROLL CURSOR FOR SELECT i::cursor_unknown FROM cursor_rows", "42704", "type \"cursor_unknown\" does not exist", "47"},
			{"FETCH FORWARD $1 FROM \"Mixed Case\"", "42601", "syntax error at or near \"$1\"", "15"},
			{"FETCH 1 FROM \"Mixed Case\" trailing", "42601", "syntax error at or near \"trailing\"", "27"},
		} {
			want := sqlCursorDiagnostic(probe.code, probe.message)
			want.Position = probe.position
			if probe.code == "42883" {
				if b.PG {
					want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				} else {
					want = sqlCursorDiagnostic("0A000", "functions are unsupported")
					want.Position = probe.position
				}
			}
			// Semantic utility errors must occur at actual Prepare, not execution.
			sqlCursorStep(t, p, b, c, fixture.Exec("SAVEPOINT analysis", 0))
			stmt, err := c.PrepareContext(p.Ctx, probe.query)
			if stmt != nil {
				stmt.Close()
				t.Fatal("invalid Prepare exposed statement")
			}
			fixture.AssertDiagnostic(t, err, &want)
			sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK TO analysis", 0))
			sqlCursorStep(t, p, b, c, fixture.Exec("RELEASE analysis", 0))
		}
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM \"Mixed Case\"", [][]any{{int64(1), "one"}}, b.PG))
	})
}
