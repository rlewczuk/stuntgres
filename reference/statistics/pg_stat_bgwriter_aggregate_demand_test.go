package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func bgwriterResetTime(t *testing.T, p *fixture.Pair, c *sql.Conn) time.Time {
	t.Helper()
	var reset time.Time
	if err := c.QueryRowContext(p.Ctx, "SELECT stats_reset FROM pg_catalog.pg_stat_bgwriter").Scan(&reset); err != nil {
		t.Fatal(err)
	}
	if reset.IsZero() {
		t.Fatal("permitted reset getter returned no genuine reset metadata")
	}
	return reset
}

func TestReferencePGStatBGWriterAggregateDemand(t *testing.T) {
	for _, mode := range []string{"cache", "snapshot"} {
		for _, retained := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/retained_%t", mode, retained), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"bgwriter_aggregate_demand"})
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
					fixture.StatisticsExec(t, p, b, c, "CREATE ROLE bgwriter_aggregate_demand", 0)
					fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency="+mode, 0)
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil { t.Fatal(err) }
					defer peer.Close()
					fixture.StatisticsExec(t, p, b, peer, "SET stats_fetch_consistency=none", 0)
					fixture.StatisticsExec(t, p, b, c, "SET ROLE bgwriter_aggregate_demand", 0)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT current_user::text AS actor", []string{"actor"}, []string{"TEXT"}, [][]any{{"bgwriter_aggregate_demand"}}))
					const query = "SELECT count(buffers_clean) FROM pg_catalog.pg_stat_bgwriter WHERE FALSE"
					for _, prepared := range []bool{false, true} {
						var held fixture.HeldStatement
						defer func() {
							if held.Statement != nil {
								held.Statement.Close()
							}
						}()
						executor := fixture.Executor(c)
						// ACL changes belong to terminal epochs, not an assumed
						// refresh of PostgreSQL tuples behind already-held locks.
						denyEpoch := func(prepare bool) {
							t.Helper()
							fixture.StatisticsExec(t, p, b, peer, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_bgwriter_buf_written_clean() FROM PUBLIC", 0)
							fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
							prior := bgwriterGet(t, p, b, peer)
							var reset time.Time
							if retained {
								reset = bgwriterResetTime(t, p, c)
							}
							cut := bgwriterSnapshotTime(t, p, c)
							if cut.Valid != (retained && mode == "snapshot") {
								t.Fatal("denied aggregate epoch initial snapshot ownership", cut)
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
								t.Fatal("peer reset did not distinguish denied aggregate provider")
							}
							fixture.StatisticsExec(t, p, b, c, "SAVEPOINT aggregate_denied", 0)
							bgwriterQueryDenied(t, p, b, executor, query, "permission denied for function pg_stat_get_bgwriter_buf_written_clean")
							fixture.StatisticsExec(t, p, b, c, "ROLLBACK TO SAVEPOINT aggregate_denied", 0)
							fixture.StatisticsExec(t, p, b, c, "RELEASE SAVEPOINT aggregate_denied", 0)
							bgwriterSnapshotUnchanged(t, p, c, cut)
							if retained && !bgwriterResetTime(t, p, c).Equal(reset) {
								t.Fatal("denial or recovery replaced positive retained reset")
							}
							presence := fixture.Select("SELECT count(*) FROM pg_catalog.pg_stat_bgwriter WHERE FALSE", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
							if prepared {
								presence = fixture.Prepared(presence)
							}
							fixture.RunStep(t, p.Ctx, c, b.Recorder, presence)
							bgwriterSnapshotUnchanged(t, p, c, cut)
							fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
						}
						denyEpoch(true)
						fixture.StatisticsExec(t, p, b, peer, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_bgwriter_buf_written_clean() TO PUBLIC", 0)
						fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
						var old bgwriterRow
						if retained {
							old = bgwriterGet(t, p, b, c)
						}
						cut := bgwriterSnapshotTime(t, p, c)
						if cut.Valid != (retained && mode == "snapshot") {
							t.Fatal("granted aggregate epoch initial snapshot ownership", cut)
						}
						const trueQuery = "SELECT count(buffers_clean) FROM pg_catalog.pg_stat_bgwriter WHERE TRUE"
						trueHeld := bgwriterHeld(t, p, c, trueQuery)
						defer trueHeld.Statement.Close()
						bgwriterSnapshotUnchanged(t, p, c, cut)
						trueExecutor := fixture.Executor(c)
						if prepared {
							trueExecutor = trueHeld
						}
						bgwriterVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('bgwriter')", "pg_stat_reset_shared")
						if current := bgwriterGet(t, p, b, peer); retained && !current.reset.After(old.reset) {
							t.Fatal("peer reset did not distinguish granted aggregate cut")
						}
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(query, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						bgwriterSnapshotUnchanged(t, p, c, cut)
						if retained {
							bgwriterSameFact(t, old, bgwriterGet(t, p, b, c))
						}
						// A second reset distinguishes an accidentally cached FALSE
						// read from the first genuine TRUE access, even in cache mode.
						bgwriterVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('bgwriter')", "pg_stat_reset_shared")
						current := bgwriterGet(t, p, b, peer)
						fixture.RunStep(t, p.Ctx, trueExecutor, b.Recorder, fixture.Select(trueQuery, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
						granted := bgwriterGet(t, p, b, c)
						if retained {
							bgwriterSameFact(t, old, granted)
						} else {
							bgwriterSameReset(t, current, granted)
						}
						positiveCut := bgwriterSnapshotTime(t, p, c)
						if positiveCut.Valid != (mode == "snapshot") {
							t.Fatal("TRUE aggregate did not perform cumulative access", positiveCut)
						}
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(query, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						bgwriterSnapshotUnchanged(t, p, c, positiveCut)
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
