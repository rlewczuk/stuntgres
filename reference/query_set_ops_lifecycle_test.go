package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestReferenceQuerySetOpsLifecycle(t *testing.T) {
	querySetOpsScenario(t, func(p *referencePair, b referenceBackend) {
		c, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		run := func(s referenceStep) {
			// Plain database/sql routes belong to DB/Tx, not *sql.Conn.
			// Context routes retain the physical owner used for cancellation.
			var source referenceExecutor = c
			if s.nonContext {
				source = b.db
			}
			referenceRunStep(t, p.ctx, source, b.recorder, s)
		}
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE set_fail(id INT)", 0),
			referenceExec("INSERT INTO set_fail VALUES(1),(2)", 2),
			referenceExec("CREATE TABLE set_atomic(id INT)", 0),
			referenceExec("INSERT INTO set_atomic VALUES(17)", 1),
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
			stmt, err := c.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal("runtime target must prepare", err)
			}
			for _, held := range []bool{false, true} {
				var rows *sql.Rows
				if held {
					rows, err = stmt.QueryContext(p.ctx)
				} else {
					rows, err = c.QueryContext(p.ctx, q)
				}
				if rows != nil {
					if !b.pg {
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
				want := controlsReferenceDiagnostic("22012", "division by zero")
				referenceAssertDiagnostic(t, err, &want)
				var result sql.Result
				if held {
					result, err = stmt.ExecContext(p.ctx)
				} else {
					result, err = c.ExecContext(p.ctx, q)
				}
				if result != nil {
					t.Fatal("failed Exec published a result")
				}
				referenceAssertDiagnostic(t, err, &want)
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
					s := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message})
					s.errorStage, s.prepared, s.nonContext = "execute", prepared, nonContext
					run(s)
					run(referenceSelect("SELECT id FROM set_atomic", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
				}
			}
		}
		q := "SELECT id AS v FROM set_fail UNION SELECT id FROM set_fail ORDER BY 1"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if b.pg {
			// The existing helper observes pg_stat_activity admission, cancels
			// server-side, and joins the operation on this SAME physical owner.
			// No native sleep capability or timing-only cancellation claim.
			for _, prepared := range []bool{false, true} {
				referenceSessionCancel(t, p.ctx, c, prepared, true)
			}
		} else {
			ctx, cancel := context.WithCancel(p.ctx)
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
		s := referenceSelect(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		run(s)
		referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, b.recorder, s)
		run(referenceExec(q, 2))
		referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, b.recorder, referenceExec(q, 2))
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
	querySetOpsScenario(t, func(p *referencePair, b referenceBackend) {
		querySetOpsSeed(t, p, b)
		for _, q := range []string{
			"SELECT 1/(v-v) AS v FROM sl UNION ALL SELECT 1/(v-v) FROM sr LIMIT 0",
			"SELECT 1/(v-v) AS v FROM sl INTERSECT ALL SELECT 1/(v-v) FROM sr LIMIT 0",
			"SELECT 1/(v-v) AS v FROM sl EXCEPT ALL SELECT 1/(v-v) FROM sr LIMIT 0",
		} {
			querySetOpsRoutes(t, p, b, querySetOpsBag(q, "v", "INT4", [][]any{}))
			querySetOpsRoutes(t, p, b, referenceExec(q, 0))
		}
		// Both arms are analyzed/admitted even when runtime demand is zero.
		q := "SELECT v FROM sl UNION ALL SELECT v FROM set_missing LIMIT 0"
		querySetOpsRoutes(t, p, b, referenceImplicitError(q, referenceDiagnostic{Code: "42P01", Message: "relation \"set_missing\" does not exist", Position: "42"}))
		q = "SELECT v FROM sl UNION ALL SELECT missing FROM sr LIMIT 0"
		querySetOpsRoutes(t, p, b, referenceImplicitError(q, referenceDiagnostic{Code: "42703", Message: "column \"missing\" does not exist", Position: "35"}))
		q = "SELECT $1 AS v UNION ALL SELECT $2::INT LIMIT 0"
		stmt, err := b.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{b.db, stmt, q}
		for _, args := range [][]any{{7, 9}, {nil, nil}} {
			s := querySetOpsBag(q, "v", "INT4", [][]any{})
			s.args = args
			referenceRunStep(t, p.ctx, held, b.recorder, s)
		}
		rows, err := stmt.QueryContext(p.ctx, 7)
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
