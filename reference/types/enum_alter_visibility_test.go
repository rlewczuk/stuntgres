package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceEnumAlterVisibilityLocks(t *testing.T) {
	enumAlterScenario(t, func(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
		for _, q := range []string{"CREATE TYPE mood AS ENUM ('z','q')", "CREATE TYPE alter_independent AS ENUM ('old')"} {
			enumAlterExec(t, f, f.A, q)
		}
		enumAlterExec(t, f, f.A, "BEGIN")
		enumAlterExec(t, f, f.A, "ALTER TYPE mood ADD VALUE 'pending'")
		// Normal inspection is nonblocking and sees only committed labels.
		enumAlterRows(t, f, f.B, "SELECT enum_last(NULL::mood) AS v", "q")
		want := enumAlterDiagnostic("22P02", `invalid input value for enum mood: "pending"`)
		want.Position = "8"
		enumAlterError(t, f, f.B, "SELECT 'pending'::mood", false, true, "execute", want)
		// A genuinely independent type can change while the first type lock is held.
		enumAlterExec(t, f, f.B, "SET lock_timeout='150ms'")
		enumAlterExec(t, f, f.B, "ALTER TYPE alter_independent ADD VALUE 'new'")
		enumAlterRows(t, f, f.B, "SELECT 'new'::alter_independent AS v", "new")
		for _, q := range []string{"ALTER TYPE mood ADD VALUE 'blocked'", "ALTER TYPE mood RENAME VALUE 'q' TO 'blocked_rename'"} {
			enumAlterError(t, f, f.B, q, false, false, "execute", enumAlterDiagnostic("55P03", "canceling statement due to lock timeout"))
		}
		enumAlterExec(t, f, f.B, "SET lock_timeout=0")
		// Observe the actual server wait before canceling. Native caller deadline
		// is the explicit adapter boundary, not an invented SQLSTATE.
		pid := 0
		if pg {
			pid = fixture.LockPID(t, f.Pair.Ctx, f.B)
		}
		ctx, cancel := context.WithTimeout(f.Pair.Ctx, 150*time.Millisecond)
		defer cancel()
		await := fixture.LockOperation(t, f.Pair.Ctx, func(parent context.Context) fixture.RelationOutcome {
			if pg {
				return fixture.RowExecute(parent, f.B, "ALTER TYPE mood ADD VALUE 'canceled'", false)
			}
			return fixture.RowExecute(ctx, f.B, "ALTER TYPE mood ADD VALUE 'canceled'", false)
		})
		if pg {
			fixture.RowWait(t, f.Pair.Ctx, pid, fixture.LockPID(t, f.Pair.Ctx, f.A))
			var ok bool
			if err := fixture.Admin.QueryRowContext(f.Pair.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&ok); err != nil || !ok {
				t.Fatal(ok, err)
			}
		}
		out := await()
		if out.Result != nil {
			t.Error("canceled ALTER exposed result")
		}
		if pg {
			got := fixture.ExtractDiagnostic(t, out.Err)
			if want := enumAlterDiagnostic("57014", "canceling statement due to user request"); got != want {
				t.Errorf("cancel got %#v want %#v", got, want)
			}
		} else if !errors.Is(out.Err, context.DeadlineExceeded) {
			t.Errorf("native blocked cancellation got %v", out.Err)
		}
		// Successful waiter must re-read predecessor's labels at lock grant.
		await = fixture.LockOperation(t, f.Pair.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			return fixture.RowExecute(ctx, f.B, "ALTER TYPE mood ADD VALUE IF NOT EXISTS 'pending' BEFORE 'absent'", true)
		})
		if pg {
			fixture.RowWait(t, f.Pair.Ctx, pid, fixture.LockPID(t, f.Pair.Ctx, f.A))
		}
		enumAlterExec(t, f, f.A, "COMMIT")
		out = await()
		if out.Err != nil {
			t.Fatal(out.Err)
		}
		if out.Result == nil {
			t.Fatal("successful waiter omitted result")
		}
		n, err := out.Result.RowsAffected()
		if err != nil || n != 0 {
			t.Fatal(n, err)
		}
		enumAlterRows(t, f, f.B, "SELECT 'pending'::mood AS v", "pending")
		for _, label := range []string{"blocked", "blocked_rename", "canceled"} {
			want := enumAlterDiagnostic("22P02", `invalid input value for enum mood: "`+label+`"`)
			want.Position = "8"
			enumAlterError(t, f, f.B, "SELECT '"+label+"'::mood", false, true, "execute", want)
		}
		// Uncommitted rename remains private; top rollback restores output and
		// removes the new label, not only a failed producer statement.
		enumAlterExec(t, f, f.A, "BEGIN")
		enumAlterExec(t, f, f.A, "ALTER TYPE mood RENAME VALUE 'q' TO 'renamed'")
		enumAlterExec(t, f, f.A, "ALTER TYPE mood ADD VALUE 'aborted'")
		enumAlterRows(t, f, f.A, "SELECT 'renamed'::mood AS v", "renamed")
		enumAlterRows(t, f, f.B, "SELECT 'q'::mood AS v", "q")
		enumAlterExec(t, f, f.A, "ROLLBACK")
		enumAlterRows(t, f, f.B, "SELECT 'q'::mood AS v", "q")
		want = enumAlterDiagnostic("22P02", `invalid input value for enum mood: "aborted"`)
		want.Position = "8"
		enumAlterError(t, f, f.B, "SELECT 'aborted'::mood", false, true, "execute", want)
	})
}
