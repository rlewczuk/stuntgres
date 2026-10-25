package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Utility validation and ordinary transaction reads have different obligations.
// The independent hypotheses below require a latest validation scan without
// replacing an RR owner's retained SELECT snapshot; pinned execution settles it.
func partitionAttachValidationSnapshots(t *testing.T) {
	for _, isolation := range []string{"REPEATABLE READ", "SERIALIZABLE"} {
		for _, defaultBranch := range []bool{false, true} {
			for _, nullRow := range []bool{false, true} {
				if defaultBranch && nullRow {
					continue
				} // NULL belongs to DEFAULT, never conflicts with explicit RANGE.
				name := isolation + "/candidate/upper"
				if defaultBranch {
					name = isolation + "/default/conflict"
				}
				if nullRow {
					name = isolation + "/candidate/null"
				}
				t.Run("validation_snapshot/"+name, func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
							run(fixture.Exec("CREATE TABLE pa_snapshot_parent(k INT) PARTITION BY RANGE(k)", 0))
							run(fixture.Exec("CREATE TABLE pa_snapshot_candidate(k INT)", 0))
							name := "pa_snapshot_candidate"
							old := int64(1)
							late := "3"
							if defaultBranch {
								name = "pa_snapshot_default"
								old = 4
								late = "2"
								run(fixture.Exec("CREATE TABLE pa_snapshot_default PARTITION OF pa_snapshot_parent DEFAULT", 0))
							}
							if nullRow {
								late = "NULL"
							}
							run(fixture.Exec("INSERT INTO "+name+" VALUES($1)", 1, old))
							owner, err := b.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer owner.Close()
							own := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, b.Recorder, s) }
							own(fixture.Exec("BEGIN ISOLATION LEVEL "+isolation, 0))
							defer owner.ExecContext(p.Ctx, "ROLLBACK")
							own(referenceRangeRows("SELECT k FROM "+name, old))
							parentRows := []int64(nil)
							if defaultBranch {
								parentRows = []int64{4}
							}
							own(referenceRangeRows("SELECT k FROM pa_snapshot_parent", parentRows...))
							run(fixture.Exec("INSERT INTO "+name+" VALUES("+late+")", 1))
							own(fixture.Exec("SAVEPOINT before_attach", 0))
							message := `partition constraint of relation "` + name + `" is violated by some row`
							if defaultBranch {
								message = `updated partition constraint for default partition "` + name + `" would be violated by some row`
							}
							s := fixture.ImplicitError("ALTER TABLE pa_snapshot_parent ATTACH PARTITION pa_snapshot_candidate FOR VALUES FROM(1) TO(3)", fixture.Diagnostic{Code: "23514", Message: message, Schema: "public", Table: name})
							t.Run("validation", func(t *testing.T) { fixture.RunStep(t, p.Ctx, owner, b.Recorder, s) })
							own(fixture.Exec("ROLLBACK TO SAVEPOINT before_attach", 0))
							own(referenceRangeRows("SELECT k FROM "+name, old))
							own(referenceRangeRows("SELECT k FROM pa_snapshot_parent", parentRows...))
							own(fixture.Exec("ROLLBACK", 0))
							run(fixture.Select("SELECT relispartition FROM pg_class WHERE relname='pa_snapshot_candidate'", []string{"relispartition"}, []string{"BOOL"}, [][]any{{false}}))
						})
					}
				})
			}
		}
	}
	for _, change := range []string{"own_good", "own_bad", "own_delete"} {
		t.Run("validation_snapshot/"+change, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE pa_snapshot_parent(k INT) PARTITION BY RANGE(k)", 0))
					run(fixture.Exec("CREATE TABLE pa_snapshot_candidate(k INT)", 0))
					run(fixture.Exec("INSERT INTO pa_snapshot_candidate VALUES(1)", 1))
					if change == "own_delete" {
						run(fixture.Exec("INSERT INTO pa_snapshot_candidate VALUES(3)", 1))
					}
					owner, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer owner.Close()
					own := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, b.Recorder, s) }
					own(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
					defer owner.ExecContext(p.Ctx, "ROLLBACK")
					want := []int64{1}
					switch change {
					case "own_good":
						own(fixture.Exec("INSERT INTO pa_snapshot_candidate VALUES(2)", 1))
						want = append(want, 2)
					case "own_bad":
						own(fixture.Exec("INSERT INTO pa_snapshot_candidate VALUES(3)", 1))
						want = append(want, 3)
					case "own_delete":
						own(fixture.Exec("DELETE FROM pa_snapshot_candidate WHERE k=3", 1))
					}
					own(fixture.Exec("SAVEPOINT before_attach", 0))
					q := "ALTER TABLE pa_snapshot_parent ATTACH PARTITION pa_snapshot_candidate FOR VALUES FROM(1) TO(3)"
					if change == "own_bad" {
						own(fixture.ImplicitError(q, fixture.Diagnostic{Code: "23514", Message: `partition constraint of relation "pa_snapshot_candidate" is violated by some row`, Schema: "public", Table: "pa_snapshot_candidate"}))
						own(fixture.Exec("ROLLBACK TO SAVEPOINT before_attach", 0))
						own(referenceRangeRows("SELECT k FROM pa_snapshot_candidate ORDER BY k", want...))
						own(fixture.Exec("ROLLBACK", 0))
					} else {
						own(fixture.Exec(q, 0))
						own(referenceRangeRows("SELECT k FROM pa_snapshot_parent ORDER BY k", want...))
						own(fixture.Exec("COMMIT", 0))
						run(referenceRangeRows("SELECT k FROM pa_snapshot_parent ORDER BY k", want...))
					}
				})
			}
		})
	}
	partitionAttachQueuedValidationSnapshot(t)
}

func partitionAttachQueuedValidationSnapshot(t *testing.T) {
	t.Run("validation_snapshot/queued_writer", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE pa_snapshot_parent(k INT) PARTITION BY RANGE(k)", 0))
				run(fixture.Exec("CREATE TABLE pa_snapshot_candidate(k INT)", 0))
				run(fixture.Exec("INSERT INTO pa_snapshot_candidate VALUES(1)", 1))
				writer, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer writer.Rollback()
				owner, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close()
				fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
				defer owner.ExecContext(p.Ctx, "ROLLBACK")
				fixture.RunStep(t, p.Ctx, owner, b.Recorder, referenceRangeRows("SELECT k FROM pa_snapshot_candidate", 1))
				fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Exec("INSERT INTO pa_snapshot_candidate VALUES(3)", 1))
				ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
				defer cancel()
				q := "ALTER TABLE pa_snapshot_parent ATTACH PARTITION pa_snapshot_candidate FOR VALUES FROM(1) TO(3)"
				type outcome struct {
					result sql.Result
					err    error
				}
				done := make(chan outcome, 1)
				joined := false
				defer func() {
					cancel()
					writer.Rollback()
					if !joined {
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("queued validation worker did not join")
						}
					}
				}()
				if b.PG {
					pid := fixture.LockPID(t, ctx, owner)
					var holder int
					if err := writer.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&holder); err != nil {
						t.Fatal(err)
					}
					go func() { r, e := owner.ExecContext(ctx, q); done <- outcome{r, e} }()
					fixture.LockWaitBehind(t, ctx, pid, holder, fixture.LockOID(t, ctx, "pa_snapshot_candidate"), "AccessExclusiveLock")
					if err := writer.Commit(); err != nil {
						t.Fatal(err)
					}
				} else {
					// Native exact queue admission has private-manager coverage; the
					// shared SQL scan sees this genuinely committed writer frontier.
					if err := writer.Commit(); err != nil {
						t.Fatal(err)
					}
					go func() { r, e := owner.ExecContext(ctx, q); done <- outcome{r, e} }()
				}
				select {
				case got := <-done:
					joined = true
					if got.result != nil {
						t.Fatal("failed attach exposed result")
					}
					fixture.AssertDiagnostic(t, got.err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `partition constraint of relation "pa_snapshot_candidate" is violated by some row`, Schema: "public", Table: "pa_snapshot_candidate"})
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("ROLLBACK", 0))
			})
		}
	})
}
