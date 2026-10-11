package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceEnumAlterDeadlock(t *testing.T) {
	enumAlterScenario(t, func(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
		for _, q := range []string{"CREATE TYPE review_left AS ENUM ('old')", "CREATE TYPE review_right AS ENUM ('old')"} {
			enumAlterExec(t, f, f.A, q)
		}
		conns := [2]*sql.Conn{f.A, f.B}
		heldB, err := f.B.PrepareContext(f.Pair.Ctx, "ALTER TYPE review_left ADD VALUE 'from_b_cross'")
		if err != nil {
			t.Fatal(err)
		}
		defer heldB.Close()
		var pids [2]int
		var typeIDs [2]int64
		var database int64
		if pg {
			for i, c := range conns {
				pids[i] = fixture.LockPID(t, f.Pair.Ctx, c)
			}
			for i, name := range []string{"review_left", "review_right"} {
				if err := f.A.QueryRowContext(f.Pair.Ctx, "SELECT oid FROM pg_type WHERE typname=$1 AND typnamespace='public'::regnamespace", name).Scan(&typeIDs[i]); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.A.QueryRowContext(f.Pair.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
		}
		for _, c := range conns {
			enumAlterExec(t, f, c, "BEGIN")
		}
		// Registered before workers: their cancellation/join runs before rollback.
		t.Cleanup(func() {
			for _, c := range conns {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				c.ExecContext(ctx, "ROLLBACK")
				cancel()
			}
		})
		enumAlterExec(t, f, f.A, "ALTER TYPE review_left ADD VALUE 'from_a_initial'")
		enumAlterExec(t, f, f.B, "ALTER TYPE review_right ADD VALUE 'from_b_initial'")
		queries := [2]string{"ALTER TYPE review_right ADD VALUE 'from_a_cross'", "ALTER TYPE review_left ADD VALUE 'from_b_cross'"}
		var failedProbe [2]error
		var rollback [2]error
		worker := func(i int) func(context.Context) fixture.RelationOutcome {
			return func(ctx context.Context) fixture.RelationOutcome {
				var out fixture.RelationOutcome
				if i == 1 {
					out.Result, out.Err = heldB.ExecContext(ctx)
				} else {
					out = fixture.RowExecute(ctx, conns[i], queries[i], false)
				}
				if out.Err != nil {
					_, failedProbe[i] = conns[i].ExecContext(ctx, "SELECT 1")
					_, rollback[i] = conns[i].ExecContext(ctx, "ROLLBACK")
				}
				return out
			}
		}
		first := fixture.LockOperation(t, f.Pair.Ctx, worker(0))
		if pg {
			fixture.RowWait(t, f.Pair.Ctx, pids[0], pids[1])
		}
		second := fixture.LockOperation(t, f.Pair.Ctx, worker(1))
		// Worker error cleanup releases the victim before joining the survivor.
		// Completion order never determines victim identity.
		outcomes := [2]fixture.RelationOutcome{first(), second()}
		victim := -1
		for i, out := range outcomes {
			if out.Err != nil {
				if victim != -1 {
					t.Fatal("two victims", outcomes[0].Err, outcomes[1].Err)
				}
				victim = i
				if out.Result != nil {
					t.Fatal("victim exposed result")
				}
			} else {
				fixture.LockAffected(t, out.Result, 0)
			}
		}
		if victim == -1 {
			t.Fatal("cycle had no victim")
		}
		if rollback[victim] != nil {
			t.Fatal("victim rollback failed", rollback[victim])
		}
		fixture.EnumReferenceAssertDiagnostic(t, failedProbe[victim], enumAlterDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
		enumAlterReviewDeadlockDiagnostic(t, outcomes[victim].Err, pg, pids, typeIDs, database, victim)
		survivor := 1 - victim
		enumAlterExec(t, f, conns[survivor], "COMMIT")
		left, right := "from_a_initial", "from_a_cross"
		if survivor == 1 {
			left, right = "from_b_cross", "from_b_initial"
		}
		for i, name := range []string{"review_left", "review_right"} {
			label := left
			if i == 1 {
				label = right
			}
			fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT enum_range(NULL::"+name+")::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"{old," + label + "}"}}))
		}
		absent := [2]string{"from_a_initial", "from_a_cross"}
		if victim == 1 {
			absent = [2]string{"from_b_cross", "from_b_initial"}
		}
		for i, name := range []string{"review_left", "review_right"} {
			want := enumAlterDiagnostic("22P02", "invalid input value for enum "+name+": \""+absent[i]+"\"")
			want.Position = "8"
			for _, prepared := range []bool{false, true} {
				phase := "execute"
				if prepared {
					phase = "prepare"
				}
				enumAlterError(t, f, f.A, "SELECT "+enumAlterReviewLiteral(absent[i])+"::"+name, prepared, true, phase, want)
			}
		}
		// Both connections mutate both type resources after victim/survivor end.
		for i, c := range conns {
			enumAlterExec(t, f, c, "SET lock_timeout='150ms'")
			enumAlterExec(t, f, c, "BEGIN")
			for _, name := range []string{"review_left", "review_right"} {
				enumAlterExec(t, f, c, "ALTER TYPE "+name+" ADD VALUE 'cleanup_"+fmt.Sprint(i)+"'")
			}
			enumAlterExec(t, f, c, "COMMIT")
		}
		for i, name := range []string{"review_left", "review_right"} {
			label := left
			if i == 1 {
				label = right
			}
			fixture.RunStep(t, f.Pair.Ctx, f.B, f.Recorder, fixture.Select("SELECT enum_range(NULL::"+name+")::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"{old," + label + ",cleanup_0,cleanup_1}"}}))
		}
	})
}

func enumAlterReviewDeadlockDiagnostic(t *testing.T, err error, pg bool, pids [2]int, types [2]int64, database int64, victim int) {
	t.Helper()
	d := fixture.ExtractDiagnostic(t, err)
	want := enumAlterDiagnostic("40P01", "deadlock detected")
	want.Hint = "See server log for query details."
	if pg {
		other := 1 - victim
		want.Detail = fmt.Sprintf("Process %d waits for ExclusiveLock on object %d of class 1247 of database %d; blocked by process %d.\nProcess %d waits for ExclusiveLock on object %d of class 1247 of database %d; blocked by process %d.", pids[victim], types[other], database, pids[other], pids[other], types[victim], database, pids[victim])
	} else {
		lines := strings.Split(d.Detail, "\n")
		pattern := regexp.MustCompile(`^Transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\) waits for ExclusiveLock on type ([1-9][0-9]*) \(class 1247, sub-ID 0, epoch ([1-9][0-9]*)\); blocked by transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\)\.$`)
		if len(lines) != 2 {
			t.Fatal("two type edges required", d.Detail)
		}
		a, b := pattern.FindStringSubmatch(lines[0]), pattern.FindStringSubmatch(lines[1])
		if a == nil || b == nil || a[1] != b[6] || a[2] != b[7] || a[3] != b[8] || b[1] != a[6] || b[2] != a[7] || b[3] != a[8] || a[1] == b[1] || a[2] == b[2] || a[4] == b[4] || a[3] != a[5] || b[3] != b[5] {
			t.Fatal("closed distinct type/transaction/session/epoch cycle required", d.Detail)
		}
		d.Detail = "" // Opaque native IDs differ; complete edge grammar/relationships checked.
	}
	if d != want {
		t.Fatalf("deadlock diagnostic got %#v want %#v", d, want)
	}
}
