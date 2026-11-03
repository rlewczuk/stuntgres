package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGINMaintenanceReturning(t *testing.T) {
	for _, route := range []string{"direct", "fresh", "held"} {
		for _, commit := range []bool{false, true} {
			name := route + "/rollback"
			if commit {
				name = route + "/commit"
			}
			t.Run(name, func(t *testing.T) {
				classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					conn, run := classConn(t, p, b)
					_, otherRun := classConn(t, p, b)
					ginMaintSeed(run, false)
					run(ginMaintPositive(ginMaintPositiveSQL))
					run(ginMaintResult(ginMaintCleanSQL, int64(0)))
					execute := func(step fixture.Step) {
						switch route {
						case "fresh":
							run(fixture.Prepared(step))
						case "held":
							fixture.RunStep(t, p.Ctx, classHeld(t, p, conn, step.Query), b.Recorder, step)
						default:
							run(step)
						}
					}
					run(fixture.Exec("BEGIN", 0))
					t.Cleanup(func() { _, _ = conn.ExecContext(p.Ctx, "ROLLBACK") })
					// There is no prior pending work. The INSERT's real index output must exist
					// before its RETURNING expression can drain it; Finish cannot manufacture it.
					execute(ginMaintPositive("INSERT INTO gin_maint_oracle VALUES(2,'{2,3}') RETURNING gin_clean_pending_list('gin_maint_oracle_i'::regclass)>0 AS cleaned"))
					execute(ginMaintResult(ginMaintCleanSQL, int64(0)))
					run(ginIDs(ginMaintIDsSQL, []int64{1, 2}))
					otherRun(ginIDs(ginMaintIDsSQL, []int64{1}))
					run(fixture.Exec("SAVEPOINT returned_insert", 0))
					execute(ginMaintPositive("UPDATE gin_maint_oracle SET a='{2,4}' WHERE id=2 RETURNING gin_clean_pending_list('gin_maint_oracle_i'::regclass)>0 AS cleaned"))
					execute(ginMaintResult(ginMaintCleanSQL, int64(0)))
					run(fixture.Exec("ROLLBACK TO returned_insert", 0))
					execute(ginMaintResult(ginMaintCleanSQL, int64(0)))
					run(ginIDs("SELECT id FROM gin_maint_oracle WHERE a @> '{3}'::integer[] ORDER BY id", []int64{2}))
					otherRun(ginIDs(ginMaintIDsSQL, []int64{1}))
					if commit {
						run(fixture.Exec("COMMIT", 0))
						otherRun(ginIDs(ginMaintIDsSQL, []int64{1, 2}))
					} else {
						run(fixture.Exec("ROLLBACK", 0))
						otherRun(ginIDs(ginMaintIDsSQL, []int64{1}))
					}
					otherRun(ginMaintResult(ginMaintCleanSQL, int64(0)))
					// Exec also reports the actual DML count when its RETURNING rows are discarded.
					execute(fixture.Exec("INSERT INTO gin_maint_oracle VALUES(3,'{2,5}') RETURNING gin_clean_pending_list('gin_maint_oracle_i'::regclass)>0 AS cleaned", 1))
					execute(ginMaintResult(ginMaintCleanSQL, int64(0)))
				})
			})
		}
	}
}
