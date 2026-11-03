package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func bgwriterSnapshotUnchanged(t *testing.T, p *fixture.Pair, c *sql.Conn, before sql.NullTime) {
	t.Helper()
	after := bgwriterSnapshotTime(t, p, c)
	if before.Valid != after.Valid || before.Valid && !before.Time.Equal(after.Time) {
		t.Fatal("statistics snapshot changed", before, after)
	}
}

func bgwriterAdmissionRead(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, query string, want bgwriterRow, retained bool) {
	t.Helper()
	rows, err := executor.QueryContext(p.Ctx, query)
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || len(names) != 2 || names[0] != "buffers_clean" || names[1] != "stats_reset" {
		t.Fatal("two-occurrence column names", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT8" || types[1].DatabaseTypeName() != "TIMESTAMPTZ" {
		t.Fatal("two-occurrence types", types, err)
	}
	if !rows.Next() { t.Fatal("missing two-occurrence singleton", rows.Err()) }
	var clean int64
	var reset time.Time
	if err := rows.Scan(&clean, &reset); err != nil { t.Fatal(err) }
	if clean < 0 || !reset.Equal(want.reset) || retained && clean != want.clean || !b.PG && clean != 0 {
		t.Fatal("two-occurrence current/retained values", clean, reset, want)
	}
	if rows.Next() || rows.Err() != nil { t.Fatal("extra two-occurrence row", rows.Err()) }
}

func bgwriterClean(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) int64 {
	t.Helper()
	var clean int64
	if err := c.QueryRowContext(p.Ctx, "SELECT buffers_clean FROM pg_catalog.pg_stat_bgwriter").Scan(&clean); err != nil {
		t.Fatal(err)
	}
	if clean < 0 || !b.PG && clean != 0 {
		t.Fatal("permitted clean counter", clean)
	}
	return clean
}

func TestReferencePGStatBGWriterQueryAdmission(t *testing.T) {
	for _, mode := range []string{"cache", "snapshot"} {
		for _, retained := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/retained_%t", mode, retained), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"bgwriter_query_admission"})
				fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					restoreACLs := bgwriterRestoreACLs(t, p, b, c)
					defer func() {
						defer restoreACLs()
						ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
						defer cancel()
						for _, query := range []string{"ROLLBACK", "RESET ROLE"} {
							if _, err := c.ExecContext(ctx, query); err != nil { t.Error(query, err) }
						}
					}()
					fixture.StatisticsExec(t, p, b, c, "CREATE ROLE bgwriter_query_admission", 0)
					fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency="+mode, 0)
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil { t.Fatal(err) }
					defer peer.Close()
					fixture.StatisticsExec(t, p, b, peer, "SET stats_fetch_consistency=none", 0)
					fixture.StatisticsExec(t, p, b, c, "SET ROLE bgwriter_query_admission", 0)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT current_user::text AS actor", []string{"actor"}, []string{"TEXT"}, [][]any{{"bgwriter_query_admission"}}))
					const query = "SELECT a.buffers_clean,b.stats_reset FROM pg_catalog.pg_stat_bgwriter a CROSS JOIN pg_catalog.pg_stat_bgwriter b"
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
							fixture.StatisticsExec(t, p, b, peer, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_bgwriter_stat_reset_time() FROM PUBLIC", 0)
							fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
							prior := bgwriterGet(t, p, b, peer)
							var clean int64
							if retained {
								clean = bgwriterClean(t, p, b, c)
							}
							cut := bgwriterSnapshotTime(t, p, c)
							if cut.Valid != (retained && mode == "snapshot") {
								t.Fatal("denied epoch initial snapshot ownership", cut)
							}
							if prepare {
								held = bgwriterHeld(t, p, c, query)
								if prepared {
									executor = held
								}
							}
							bgwriterSnapshotUnchanged(t, p, c, cut)
							bgwriterVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('bgwriter')", "pg_stat_reset_shared")
							if current := bgwriterGet(t, p, b, peer); !current.reset.After(prior.reset) {
								t.Fatal("peer reset did not distinguish denied epoch provider")
							}
							fixture.StatisticsExec(t, p, b, c, "SAVEPOINT query_denied", 0)
							bgwriterQueryDenied(t, p, b, executor, query, "permission denied for function pg_stat_get_bgwriter_stat_reset_time")
							fixture.StatisticsExec(t, p, b, c, "ROLLBACK TO SAVEPOINT query_denied", 0)
							fixture.StatisticsExec(t, p, b, c, "RELEASE SAVEPOINT query_denied", 0)
							bgwriterSnapshotUnchanged(t, p, c, cut)
							if retained && bgwriterClean(t, p, b, c) != clean {
								t.Fatal("denial or recovery changed retained permitted counter")
							}
							bgwriterSnapshotUnchanged(t, p, c, cut)
							fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
						}
						denyEpoch(true)
						fixture.StatisticsExec(t, p, b, peer, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_bgwriter_stat_reset_time() TO PUBLIC", 0)
						fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
						var old bgwriterRow
						if retained {
							old = bgwriterGet(t, p, b, c)
						}
						bgwriterVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('bgwriter')", "pg_stat_reset_shared")
						current := bgwriterGet(t, p, b, peer)
						want := current
						if retained {
							if !current.reset.After(old.reset) {
								t.Fatal("peer reset did not distinguish granted retained cut")
							}
							want = old
						}
						// Reuse the statement prepared while EXECUTE was revoked.
						bgwriterAdmissionRead(t, p, b, executor, query, want, retained)
						granted := bgwriterGet(t, p, b, c)
						positiveCut := bgwriterSnapshotTime(t, p, c)
						if positiveCut.Valid != (mode == "snapshot") {
							t.Fatal("grant did not capture expected Reader", positiveCut)
						}
						bgwriterVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('bgwriter')", "pg_stat_reset_shared")
						bgwriterSameFact(t, granted, bgwriterGet(t, p, b, c))
						bgwriterSnapshotUnchanged(t, p, c, positiveCut)
						fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
						denyEpoch(false)
					}
				})
			})
		}
	}
}
