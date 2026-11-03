package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGINMaintenanceTransactions(t *testing.T) {
	// Each case creates its own fixture and backend subtests. A fatal native
	// unsupported error cannot prevent any PostgreSQL case from executing.
	for _, name := range []string{
		"GinMaintReadOnlyExistingPending",
		"GinMaintRollbackAfterClean",
		"GinMaintSavepointRollbackAfterClean",
		"GinMaintFailedSelectAfterClean",
		"GinMaintOwnInsertRollbackAfterClean",
		"GinMaintOwnInsertCommitAfterClean",
		"GinMaintConcurrentPrivateCommitAfterClean",
		"GinMaintReadOnlyBuiltOff",
		"GinMaintNullReadOnly",
		"GinMaintConcurrentPresentationCommitAfterClean",
		"GinMaintConcurrentPresentationRollbackAfterClean",
		"GinMaintConcurrentPresentationSavepointAfterClean",
		"GinMaintForeignCommittedPresentationAfterClean",
	} {
		t.Run(name, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				ginMaintSeed(run, name == "GinMaintReadOnlyBuiltOff")
				run(ginIDs(ginMaintIDsSQL, []int64{1}))
				// Return the session to idle even on a diagnostic/assertion failure.
				t.Cleanup(func() { _, _ = conn.ExecContext(p.Ctx, "ROLLBACK") })
				switch name {
				case "GinMaintReadOnlyExistingPending":
					run(fixture.Exec("BEGIN READ ONLY", 0))
					run(ginMaintPositive(ginMaintPositiveSQL))
					run(ginMaintResult(ginMaintCleanSQL, int64(0)))
					run(ginIDs(ginMaintIDsSQL, []int64{1}))
					run(fixture.Exec("COMMIT", 0))
				case "GinMaintRollbackAfterClean":
					run(fixture.Exec("BEGIN", 0))
					run(ginMaintPositive(ginMaintPositiveSQL))
					run(fixture.Exec("ROLLBACK", 0))
					run(ginMaintResult(ginMaintCleanSQL, int64(0)))
				case "GinMaintSavepointRollbackAfterClean":
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("SAVEPOINT before_clean", 0))
					run(ginMaintPositive(ginMaintPositiveSQL))
					run(fixture.Exec("ROLLBACK TO SAVEPOINT before_clean", 0))
					run(ginMaintResult(ginMaintCleanSQL, int64(0)))
					run(ginIDs(ginMaintIDsSQL, []int64{1}))
					run(fixture.Exec("COMMIT", 0))
				case "GinMaintFailedSelectAfterClean":
					run(fixture.Exec("BEGIN", 0))
					// The runtime denominator depends on a MATERIALIZED volatile
					// call: no planner-foldable 1/0 can bypass cleanup. Moreover,
					// only a POSITIVE cleanup enters the failing CASE arm. Seeing
					// 22012 therefore witnesses that real pending work ran first.
					q := `WITH cleaned AS MATERIALIZED (
 SELECT gin_clean_pending_list('gin_maint_oracle_i'::regclass) AS pages
) SELECT CASE WHEN pages > 0 THEN pages / (pages - pages) ELSE NULL END AS failed FROM cleaned`
					d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
					s := fixture.ErrorDiagnostic(q, d)
					s.ErrorStage = "execute"
					run(s)
					run(fixture.Exec("ROLLBACK", 0))
					run(ginMaintResult(ginMaintCleanSQL, int64(0)))
				case "GinMaintOwnInsertRollbackAfterClean", "GinMaintOwnInsertCommitAfterClean":
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO gin_maint_oracle VALUES(2,'{2,3}')", 1))
					run(ginIDs(ginMaintIDsSQL, []int64{1, 2}))
					other, otherRun := classConn(t, p, b)
					otherRun(ginIDs(ginMaintIDsSQL, []int64{1}))
					run(ginMaintPositive(ginMaintPositiveSQL))
					run(ginMaintResult(ginMaintCleanSQL, int64(0)))
					// Physical cleanup must not publish the caller's private row.
					otherRun(ginIDs(ginMaintIDsSQL, []int64{1}))
					if name == "GinMaintOwnInsertCommitAfterClean" {
						run(fixture.Exec("COMMIT", 0))
						otherRun(ginIDs(ginMaintIDsSQL, []int64{1, 2}))
					} else {
						run(fixture.Exec("ROLLBACK", 0))
						otherRun(ginIDs(ginMaintIDsSQL, []int64{1}))
					}
					fixture.RunStep(t, p.Ctx, other, b.Recorder, ginMaintResult(ginMaintCleanSQL, int64(0)))
				case "GinMaintConcurrentPrivateCommitAfterClean":
					// A really inserts before B cleans; no sleep, mock journal or
					// backend filter. PG physically appends uncommitted postings,
					// so B can drain them without exposing their SQL rows.
					other, otherRun := classConn(t, p, b)
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO gin_maint_oracle VALUES(2,'{2,3}')", 1))
					run(ginIDs(ginMaintIDsSQL, []int64{1, 2}))
					otherRun(ginIDs(ginMaintIDsSQL, []int64{1}))
					otherRun(ginMaintPositive(ginMaintPositiveSQL))
					otherRun(ginMaintResult(ginMaintCleanSQL, int64(0)))
					otherRun(ginIDs(ginMaintIDsSQL, []int64{1}))
					run(ginIDs(ginMaintIDsSQL, []int64{1, 2}))
					run(fixture.Exec("COMMIT", 0))
					otherRun(ginIDs(ginMaintIDsSQL, []int64{1, 2}))
					// Committing A must not resurrect the split captured before B.
					fixture.RunStep(t, p.Ctx, other, b.Recorder, ginMaintResult(ginMaintCleanSQL, int64(0)))
					otherRun(fixture.Exec("INSERT INTO gin_maint_oracle VALUES(3,'{2,4}')", 1))
					otherRun(ginMaintPositive(ginMaintPositiveSQL))
					otherRun(ginMaintResult(ginMaintCleanSQL, int64(0)))
					otherRun(ginIDs(ginMaintIDsSQL, []int64{1, 2, 3}))
				case "GinMaintReadOnlyBuiltOff":
					run(fixture.Exec("BEGIN READ ONLY", 0))
					run(ginMaintResult(ginMaintCleanSQL, int64(0)))
					run(fixture.Exec("COMMIT", 0))
				case "GinMaintNullReadOnly":
					run(fixture.Exec("BEGIN READ ONLY", 0))
					run(ginMaintResult("SELECT gin_clean_pending_list(NULL)", nil))
					run(ginMaintResult("SELECT gin_clean_pending_list(NULL::regclass)", nil))
					run(fixture.Exec("COMMIT", 0))
					run(ginMaintPositive(ginMaintPositiveSQL))
					run(ginMaintResult(ginMaintCleanSQL, int64(0)))
				case "GinMaintConcurrentPresentationCommitAfterClean", "GinMaintConcurrentPresentationRollbackAfterClean", "GinMaintConcurrentPresentationSavepointAfterClean":
					_, otherRun := classConn(t, p, b)
					t.Cleanup(func() {
						_, _ = conn.ExecContext(p.Ctx, "ROLLBACK")
						var exists bool
						if err := conn.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_opclass WHERE opcname='gin_maint_presented_array' AND opcmethod=2742)").Scan(&exists); err != nil {
							t.Error(err)
							return
						}
						if exists {
							if _, err := conn.ExecContext(p.Ctx, "ALTER OPERATOR CLASS pg_catalog.gin_maint_presented_array USING gin RENAME TO array_ops"); err != nil {
								t.Error(err)
							}
						}
					})
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO gin_maint_oracle VALUES(2,'{2,3}')", 1))
					run(fixture.Exec("SAVEPOINT before_presentation", 0))
					run(fixture.Exec("ALTER OPERATOR CLASS pg_catalog.array_ops USING gin RENAME TO gin_maint_presented_array", 0))
					run(fixture.Exec("INSERT INTO gin_maint_oracle VALUES(3,'{2,4}')", 1))
					run(ginIDs(ginMaintIDsSQL, []int64{1, 2, 3}))
					otherRun(ginIDs(ginMaintIDsSQL, []int64{1}))
					otherRun(ginMaintPositive(ginMaintPositiveSQL))
					otherRun(ginMaintResult(ginMaintCleanSQL, int64(0)))
					otherRun(ginIDs(ginMaintIDsSQL, []int64{1}))
					if name == "GinMaintConcurrentPresentationRollbackAfterClean" {
						run(fixture.Exec("ROLLBACK", 0))
					} else {
						if name == "GinMaintConcurrentPresentationSavepointAfterClean" {
							run(fixture.Exec("ROLLBACK TO before_presentation", 0))
							run(ginIDs(ginMaintIDsSQL, []int64{1, 2}))
						}
						run(fixture.Exec("COMMIT", 0))
					}
					otherRun(ginMaintResult(ginMaintCleanSQL, int64(0)))
				case "GinMaintForeignCommittedPresentationAfterClean":
					_, otherRun := classConn(t, p, b)
					t.Cleanup(func() {
						_, _ = conn.ExecContext(p.Ctx, "ROLLBACK")
						var exists bool
						if err := conn.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_opclass WHERE opcname='gin_maint_foreign_array' AND opcmethod=2742)").Scan(&exists); err != nil {
							t.Error(err)
							return
						}
						if exists {
							if _, err := conn.ExecContext(p.Ctx, "ALTER OPERATOR CLASS pg_catalog.gin_maint_foreign_array USING gin RENAME TO array_ops"); err != nil {
								t.Error(err)
							}
						}
					})
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO gin_maint_oracle VALUES(2,'{2,3}')", 1))
					run(fixture.Exec("SAVEPOINT accepted_before_peer", 0))
					otherRun(fixture.Exec("ALTER OPERATOR CLASS pg_catalog.array_ops USING gin RENAME TO gin_maint_foreign_array", 0))
					otherRun(ginMaintPositive(ginMaintPositiveSQL))
					otherRun(ginMaintResult(ginMaintCleanSQL, int64(0)))
					otherRun(ginIDs(ginMaintIDsSQL, []int64{1}))
					run(fixture.Exec("ROLLBACK TO accepted_before_peer", 0))
					run(fixture.Exec("COMMIT", 0))
					otherRun(ginMaintResult(ginMaintCleanSQL, int64(0)))
				default:
					t.Fatalf("unimplemented transaction contract %q", name)
				}
				switch name {
				case "GinMaintOwnInsertCommitAfterClean":
					run(ginIDs(ginMaintIDsSQL, []int64{1, 2}))
				case "GinMaintConcurrentPrivateCommitAfterClean":
					run(ginIDs(ginMaintIDsSQL, []int64{1, 2, 3}))
				case "GinMaintConcurrentPresentationCommitAfterClean":
					run(ginIDs(ginMaintIDsSQL, []int64{1, 2, 3}))
				case "GinMaintConcurrentPresentationSavepointAfterClean", "GinMaintForeignCommittedPresentationAfterClean":
					run(ginIDs(ginMaintIDsSQL, []int64{1, 2}))
				default:
					run(ginIDs(ginMaintIDsSQL, []int64{1}))
				}
			})
		})
	}
}
