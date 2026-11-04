package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGReplicationSlotsDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { slotsRun(t, p, b, c, s) }
			run(fixture.Exec("CREATE TABLE slots_diag_sentinel(n integer)", 0))
			run(fixture.Exec("INSERT INTO slots_diag_sentinel VALUES(41)", 1))
			check := func() {
				slotsEmpty(t, p, b, c, slotsSelect)
				run(fixture.Select("SELECT n FROM slots_diag_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(41)}}))
			}
			for _, tc := range []struct{ query, code, message, position string }{
				{"SELECT missing FROM pg_replication_slots", "42703", `column "missing" does not exist`, "8"},
				{"SELECT missing FROM pg_catalog.pg_replication_slots WHERE FALSE", "42703", `column "missing" does not exist`, "8"},
				{"SELECT s.missing FROM pg_catalog.pg_replication_slots s", "42703", "column s.missing does not exist", "8"},
				{"SELECT * FROM pg_catalog.pg_replication_slots_missing", "42P01", `relation "pg_catalog.pg_replication_slots_missing" does not exist`, "15"},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.SelectRows, s.Prepared = true, prepared
					if prepared { s.ErrorStage = "prepare" }
					run(s)
					check()
				}
			}
			for _, tc := range []struct{ query, code, message, detail, hint string }{
				{"INSERT INTO pg_replication_slots(slot_name) VALUES('x')", "55000", `cannot insert into view "pg_replication_slots"`, "Views that do not select from a single table or view are not automatically updatable.", "To enable inserting into the view, provide an INSTEAD OF INSERT trigger or an unconditional ON INSERT DO INSTEAD rule."},
				{"UPDATE pg_replication_slots SET active=false", "55000", `cannot update view "pg_replication_slots"`, "Views that do not select from a single table or view are not automatically updatable.", "To enable updating the view, provide an INSTEAD OF UPDATE trigger or an unconditional ON UPDATE DO INSTEAD rule."},
				{"DELETE FROM pg_replication_slots", "55000", `cannot delete from view "pg_replication_slots"`, "Views that do not select from a single table or view are not automatically updatable.", "To enable deleting from the view, provide an INSTEAD OF DELETE trigger or an unconditional ON DELETE DO INSTEAD rule."},
				{"TRUNCATE pg_replication_slots", "42809", `"pg_replication_slots" is not a table`, "", ""},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Hint: tc.hint})
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared && tc.code == "55000" { s.ErrorStage = "prepare" }
					run(s)
					check()
				}
			}
			copyPool := fixture.CopyScenarioPool(t, p, b.PG, "", "")
			copyConn := fixture.SequenceCoreConn(t, p.Ctx, copyPool)
			result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_replication_slots FROM STDIN", strings.NewReader("x\n"))
			want := fixture.ControlsReferenceDiagnostic("42809", `cannot copy to view "pg_replication_slots"`)
			want.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
			fixture.AssertDiagnostic(t, err, &want)
			if result.Count != 0 || result.CommandTag != "" { t.Fatal(result) }
			check()
			result, err = fixture.CopyTo(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_replication_slots TO STDOUT", io.Discard)
			want = fixture.ControlsReferenceDiagnostic("42809", `cannot copy from view "pg_replication_slots"`)
			want.Hint = "Try the COPY (SELECT ...) TO variant."
			fixture.AssertDiagnostic(t, err, &want)
			if result.Count != 0 || result.CommandTag != "" { t.Fatal(result) }
			check()
			for _, route := range []string{"direct", "held"} {
				t.Run("pre_canceled_"+route, func(t *testing.T) {
					// Close rows and held handles, then return this interrupted
					// borrower before checking health on the independent owner.
					probe := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
					var stmt *sql.Stmt
					if route == "held" {
						var prepareErr error
						stmt, prepareErr = probe.PrepareContext(p.Ctx, slotsSelect)
						if prepareErr != nil { t.Fatal(prepareErr) }
						defer stmt.Close()
					}
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					var rows *sql.Rows
					var queryErr error
					if stmt != nil {
						rows, queryErr = stmt.QueryContext(ctx)
					} else {
						rows, queryErr = probe.QueryContext(ctx, slotsSelect)
					}
					slotsCanceledRead(t, rows, queryErr, b.PG)
				})
				check()
			}
		})
	}
}

// A fast pq command can finish, observe client cancellation, or receive the
// server's exact user-request cancellation. No other SQL/transport error is a
// valid outcome. Native admission must reject synchronously without rows.
func slotsCancellationError(t *testing.T, err error, pg bool) {
	t.Helper()
	if !pg || errors.Is(err, context.Canceled) {
		fixture.LexicalOrdinary(t, err, context.Canceled)
		return
	}
	want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
	fixture.AssertDiagnostic(t, err, &want)
}

func slotsCanceledRead(t *testing.T, rows *sql.Rows, err error, pg bool) {
	t.Helper()
	if rows != nil {
		defer func() {
			if closeErr := rows.Close(); closeErr != nil {
				slotsCancellationError(t, closeErr, pg)
			}
		}()
	}
	if !pg {
		if rows != nil { t.Fatal("native pre-canceled read published rows") }
		slotsCancellationError(t, err, false)
		return
	}
	if err != nil {
		slotsCancellationError(t, err, true)
		return
	}
	if rows == nil { t.Fatal("successful canceled query lacks rows") }
	slotsMetadata(t, rows, true)
	if rows.Next() { t.Fatal("canceled query fabricated slot") }
	if rowErr := rows.Err(); rowErr != nil {
		slotsCancellationError(t, rowErr, true)
	}
}
