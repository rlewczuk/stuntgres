package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func checkpointerSnapshotUnchanged(t *testing.T, p *fixture.Pair, c *sql.Conn, before sql.NullTime) {
	t.Helper()
	after := checkpointerSnapshotTime(t, p, c)
	if before.Valid != after.Valid || before.Valid && !before.Time.Equal(after.Time) {
		t.Fatal("statistics snapshot changed", before, after)
	}
}

func checkpointerAdmissionRead(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, query string, want checkpointerRow, retained bool) {
	t.Helper()
	rows, err := executor.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || len(names) != 2 || names[0] != "num_timed" || names[1] != "stats_reset" {
		t.Fatal("two-occurrence column names", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT8" || types[1].DatabaseTypeName() != "TIMESTAMPTZ" {
		t.Fatal("two-occurrence types", types, err)
	}
	if !rows.Next() {
		t.Fatal("missing two-occurrence singleton", rows.Err())
	}
	var clean int64
	var reset time.Time
	if err := rows.Scan(&clean, &reset); err != nil {
		t.Fatal(err)
	}
	if clean < 0 || !reset.Equal(want.reset) || retained && clean != want.clean || !b.PG && clean != 0 {
		t.Fatal("two-occurrence current/retained values", clean, reset, want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra two-occurrence row", rows.Err())
	}
}

func checkpointerClean(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) int64 {
	t.Helper()
	var clean int64
	if err := c.QueryRowContext(p.Ctx, "SELECT num_timed FROM pg_catalog.pg_stat_checkpointer").Scan(&clean); err != nil {
		t.Fatal(err)
	}
	if clean < 0 || !b.PG && clean != 0 {
		t.Fatal("permitted clean counter", clean)
	}
	return clean
}

func TestReferencePGStatCheckpointerQueryAdmission(t *testing.T) {
	t.Run("cross join", func(t *testing.T) { checkpointerQueryAdmission(t, false) })
	t.Run("stored view invoker", func(t *testing.T) { checkpointerQueryAdmission(t, true) })
}

func checkpointerQueryAdmission(t *testing.T, nested bool) {
	for _, mode := range []string{"cache", "snapshot"} {
		for _, retained := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/retained_%t", mode, retained), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"checkpointer_query_admission"})
				fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					restoreACLs := checkpointerRestoreACLs(t, p, b, c)
					defer func() {
						defer restoreACLs()
						ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
						defer cancel()
						for _, query := range []string{"ROLLBACK", "RESET ROLE"} {
							if _, err := c.ExecContext(ctx, query); err != nil {
								t.Error(query, err)
							}
						}
					}()
					fixture.StatisticsExec(t, p, b, c, "CREATE ROLE checkpointer_query_admission", 0)
					if nested {
						fixture.StatisticsExec(t, p, b, c, "GRANT USAGE ON SCHEMA public TO checkpointer_query_admission", 0)
						fixture.StatisticsExec(t, p, b, c, "CREATE VIEW checkpointer_admission_nested AS SELECT stats_reset FROM pg_catalog.pg_stat_checkpointer", 0)
						fixture.StatisticsExec(t, p, b, c, "GRANT SELECT ON checkpointer_admission_nested TO checkpointer_query_admission", 0)
					}
					fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency="+mode, 0)
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					fixture.StatisticsExec(t, p, b, peer, "SET stats_fetch_consistency=none", 0)
					fixture.StatisticsExec(t, p, b, c, "SET ROLE checkpointer_query_admission", 0)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT current_user::text AS actor", []string{"actor"}, []string{"TEXT"}, [][]any{{"checkpointer_query_admission"}}))
					query := "SELECT a.num_timed,b.stats_reset FROM pg_catalog.pg_stat_checkpointer a CROSS JOIN pg_catalog.pg_stat_checkpointer b"
					if nested {
						query = "SELECT a.num_timed,b.stats_reset FROM pg_catalog.pg_stat_checkpointer a CROSS JOIN checkpointer_admission_nested b"
					}
					for _, prepared := range []bool{false, true} {
						var held fixture.HeldStatement
						defer func() {
							if held.Statement != nil {
								held.Statement.Close()
							}
						}()
						executor := fixture.Executor(c)
						// Commit peer ACL changes before BEGIN: PostgreSQL's retained
						// catalog tuples need not refresh under already-held locks.
						denyEpoch := func(prepare bool) {
							t.Helper()
							fixture.StatisticsExec(t, p, b, peer, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_checkpointer_stat_reset_time() FROM PUBLIC", 0)
							fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
							prior := checkpointerGet(t, p, b, peer)
							var clean int64
							if retained {
								clean = checkpointerClean(t, p, b, c)
							}
							cut := checkpointerSnapshotTime(t, p, c)
							if cut.Valid != (retained && mode == "snapshot") {
								t.Fatal("denied epoch initial snapshot ownership", cut)
							}
							if prepare {
								held = checkpointerHeld(t, p, c, query)
								if prepared {
									executor = held
								}
							}
							checkpointerSnapshotUnchanged(t, p, c, cut)
							checkpointerVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('checkpointer')", "pg_stat_reset_shared")
							if current := checkpointerGet(t, p, b, peer); !current.reset.After(prior.reset) {
								t.Fatal("peer reset did not distinguish denied epoch provider")
							}
							fixture.StatisticsExec(t, p, b, c, "SAVEPOINT query_denied", 0)
							checkpointerQueryDenied(t, p, b, executor, query, "permission denied for function pg_stat_get_checkpointer_stat_reset_time")
							fixture.StatisticsExec(t, p, b, c, "ROLLBACK TO SAVEPOINT query_denied", 0)
							fixture.StatisticsExec(t, p, b, c, "RELEASE SAVEPOINT query_denied", 0)
							checkpointerSnapshotUnchanged(t, p, c, cut)
							if retained && checkpointerClean(t, p, b, c) != clean {
								t.Fatal("denial or recovery changed retained permitted counter")
							}
							checkpointerSnapshotUnchanged(t, p, c, cut)
							fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
						}
						denyEpoch(true)
						fixture.StatisticsExec(t, p, b, peer, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_checkpointer_stat_reset_time() TO PUBLIC", 0)
						fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
						var old checkpointerRow
						if retained {
							old = checkpointerGet(t, p, b, c)
						}
						checkpointerVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('checkpointer')", "pg_stat_reset_shared")
						current := checkpointerGet(t, p, b, peer)
						want := current
						if retained {
							if !current.reset.After(old.reset) {
								t.Fatal("peer reset did not distinguish granted retained cut")
							}
							want = old
						}
						// Reuse the statement prepared while EXECUTE was revoked.
						checkpointerAdmissionRead(t, p, b, executor, query, want, retained)
						granted := checkpointerGet(t, p, b, c)
						positiveCut := checkpointerSnapshotTime(t, p, c)
						if positiveCut.Valid != (mode == "snapshot") {
							t.Fatal("grant did not capture expected Reader", positiveCut)
						}
						checkpointerVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('checkpointer')", "pg_stat_reset_shared")
						checkpointerSameFact(t, granted, checkpointerGet(t, p, b, c))
						checkpointerSnapshotUnchanged(t, p, c, positiveCut)
						fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
						denyEpoch(false)
					}
				})
			})
		}
	}
}
