package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQuerySetOpsLifecycle(t *testing.T) {
	querySetOpsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		run := func(s fixture.Step) {
			// Plain database/sql routes belong to DB/Tx, not *sql.Conn.
			// Context routes retain the physical owner used for cancellation.
			var source fixture.Executor = c
			if s.NonContext {
				source = b.DB
			}
			fixture.RunStep(t, p.Ctx, source, b.Recorder, s)
		}
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE set_fail(id INT)", 0),
			fixture.Exec("INSERT INTO set_fail VALUES(1),(2)", 2),
			fixture.Exec("CREATE TABLE set_atomic(id INT)", 0),
			fixture.Exec("INSERT INTO set_atomic VALUES(17)", 1),
		} {
			run(s)
		}
		for _, q := range []string{
			"SELECT 1/(id-2) AS v FROM set_fail UNION ALL SELECT 3 ORDER BY 1",
			"SELECT 3 AS v UNION ALL SELECT 1/(id-2) FROM set_fail ORDER BY 1",
			"SELECT 3 AS v UNION SELECT 1/(id-2) FROM set_fail ORDER BY 1",
			"SELECT id AS v FROM set_fail INTERSECT ALL SELECT 1/(id-2) FROM set_fail ORDER BY 1",
			"SELECT id AS v FROM set_fail EXCEPT ALL SELECT 1/(id-2) FROM set_fail ORDER BY 1",
		} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("runtime target must prepare", err)
			}
			for _, held := range []bool{false, true} {
				var rows *sql.Rows
				if held {
					rows, err = stmt.QueryContext(p.Ctx)
				} else {
					rows, err = c.QueryContext(p.Ctx, q)
				}
				if rows != nil {
					if !b.PG {
						rows.Close()
						t.Fatal("native failure published rows")
					}
					if rows.Next() {
						rows.Close()
						t.Fatal("ordered set failure published a partial row")
					}
					err = rows.Err()
					if closeErr := rows.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
				}
				want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				fixture.AssertDiagnostic(t, err, &want)
				var result sql.Result
				if held {
					result, err = stmt.ExecContext(p.Ctx)
				} else {
					result, err = c.ExecContext(p.Ctx, q)
				}
				if result != nil {
					t.Fatal("failed Exec published a result")
				}
				fixture.AssertDiagnostic(t, err, &want)
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct{ query, code, message string }{
			{"INSERT INTO set_atomic SELECT id FROM set_fail UNION ALL SELECT 1/(id-2) FROM set_fail", "22012", "division by zero"},
			{"INSERT INTO set_atomic SELECT id FROM set_fail UNION ALL SELECT 2147483648::BIGINT", "22003", "integer out of range"},
		} {
			for _, prepared := range []bool{false, true} {
				for _, nonContext := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.ErrorStage, s.Prepared, s.NonContext = "execute", prepared, nonContext
					run(s)
					run(fixture.Select("SELECT id FROM set_atomic", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
				}
			}
		}
		q := "SELECT id AS v FROM set_fail UNION SELECT id FROM set_fail ORDER BY 1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if b.PG {
			// The existing helper observes pg_stat_activity admission, cancels
			// server-side, and joins the operation on this SAME physical owner.
			// No native sleep capability or timing-only cancellation claim.
			for _, prepared := range []bool{false, true} {
				fixture.SessionCancel(t, p.Ctx, c, prepared, true)
			}
		} else {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, held := range []bool{false, true} {
				var rows *sql.Rows
				if held {
					rows, err = stmt.QueryContext(ctx)
				} else {
					rows, err = c.QueryContext(ctx, q)
				}
				if rows != nil {
					rows.Close()
					t.Fatal("canceled query published rows")
				}
				querySetOpsOrdinaryCancel(t, err)
				var result sql.Result
				if held {
					result, err = stmt.ExecContext(ctx)
				} else {
					result, err = c.ExecContext(ctx, q)
				}
				if result != nil {
					t.Fatal("canceled Exec published result")
				}
				querySetOpsOrdinaryCancel(t, err)
			}
		}
		s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		run(s)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, q}, b.Recorder, s)
		run(fixture.Exec(q, 2))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, q}, b.Recorder, fixture.Exec(q, 2))
	})
}

func querySetOpsOrdinaryCancel(t *testing.T, err error) {
	t.Helper()
	var state interface{ SQLState() string }
	if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
		t.Fatalf("context cancellation must remain ordinary: %T %v", err, err)
	}
}

func TestReferenceQuerySetOpsDemandAdmission(t *testing.T) {
	querySetOpsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		querySetOpsSeed(t, p, b)
		for _, q := range []string{
			"SELECT 1/(v-v) AS v FROM sl UNION ALL SELECT 1/(v-v) FROM sr LIMIT 0",
			"SELECT 1/(v-v) AS v FROM sl INTERSECT ALL SELECT 1/(v-v) FROM sr LIMIT 0",
			"SELECT 1/(v-v) AS v FROM sl EXCEPT ALL SELECT 1/(v-v) FROM sr LIMIT 0",
		} {
			querySetOpsRoutes(t, p, b, querySetOpsBag(q, "v", "INT4", [][]any{}))
			querySetOpsRoutes(t, p, b, fixture.Exec(q, 0))
		}
		// Both arms are analyzed/admitted even when runtime demand is zero.
		q := "SELECT v FROM sl UNION ALL SELECT v FROM set_missing LIMIT 0"
		querySetOpsRoutes(t, p, b, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P01", Message: "relation \"set_missing\" does not exist", Position: "42"}))
		q = "SELECT v FROM sl UNION ALL SELECT missing FROM sr LIMIT 0"
		querySetOpsRoutes(t, p, b, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42703", Message: "column \"missing\" does not exist", Position: "35"}))
		q = "SELECT $1 AS v UNION ALL SELECT $2::INT LIMIT 0"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{b.DB, stmt, q}
		for _, args := range [][]any{{7, 9}, {nil, nil}} {
			s := querySetOpsBag(q, "v", "INT4", [][]any{})
			s.Args = args
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		rows, err := stmt.QueryContext(p.Ctx, 7)
		if rows != nil {
			rows.Close()
			t.Fatal("missing undemanded argument published rows")
		}
		var state interface{ SQLState() string }
		if err == nil || errors.As(err, &state) || err.Error() != "sql: expected 2 arguments, got 1" {
			t.Fatal("held argument admission must remain ordinary", err)
		}
	})
}
