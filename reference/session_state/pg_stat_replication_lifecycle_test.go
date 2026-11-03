package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGStatReplicationLifecycle(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		held := replicationHeld(t, p, c, replicationSQL)
		defer held.Statement.Close()
		replicationEmpty(t, p, b, held)
		peer := statActivityConn(t, p, b)
		defer peer.ExecContext(context.Background(), "ROLLBACK")
		for _, command := range []struct {
			query string
			count int64
		}{
			{"CREATE TABLE replication_sentinel(v integer)", 0},
			{"INSERT INTO replication_sentinel VALUES(37)", 1},
			{"SET application_name='ordinary replication observer'", 0},
			{"SET synchronous_commit=off", 0},
			{"SET synchronous_commit=on", 0},
			{"BEGIN", 0},
			{"UPDATE replication_sentinel SET v=41", 1},
			{"ROLLBACK", 0},
		} {
			statsExec(t, p, b, peer, command.query, command.count)
			replicationEmpty(t, p, b, c)
			replicationEmpty(t, p, b, held)
		}
		replicationSentinel(t, p, b, c)
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, executor := range []fixture.Executor{c, held} {
				rows, err := executor.QueryContext(ctx, replicationSQL)
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled query returned rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary context cancellation", err)
				}
			}
			replicationEmpty(t, p, b, held)
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "pg_stat_replication") {
				t.Fatal("Dump serialized monitoring relation", dump.String())
			}
			for _, restore := range []bool{false, true} {
				retained, err := held.QueryContext(p.Ctx, replicationSQL)
				if err != nil {
					t.Fatal(err)
				}
				if restore {
					err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
				} else {
					err = stuntgres.Reset(p.Ctx, p.Native)
				}
				if err != nil {
					retained.Close()
					t.Fatal(err)
				}
				replicationMetadata(t, retained, false, replicationAllColumns())
				if retained.Next() || retained.Err() != nil {
					retained.Close()
					t.Fatal("retained empty generation", retained.Err())
				}
				retained.Close()
				// Existing statement revalidation selects the newly owned view;
				// the previous Rows retain their own immutable descriptor above.
				replicationEmpty(t, p, b, held)
				replicationEmpty(t, p, b, c)
				if restore {
					replicationSentinel(t, p, b, c)
				} else {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_class WHERE relname='replication_sentinel' AND relnamespace='public'::regnamespace", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
			}
		}
	})
}
