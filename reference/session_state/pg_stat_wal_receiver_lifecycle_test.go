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

func TestReferencePGStatWALReceiverLifecycle(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		held := walReceiverHeld(t, p, c, walReceiverSQL)
		defer held.Statement.Close()
		walReceiverEmpty(t, p, b, held)
		peer := statActivityConn(t, p, b)
		defer peer.ExecContext(context.Background(), "ROLLBACK")
		for _, command := range []struct {
			query string
			count int64
		}{
			{"CREATE TABLE wal_receiver_sentinel(v integer)", 0},
			{"INSERT INTO wal_receiver_sentinel VALUES(37)", 1},
			{"SET application_name='ordinary WAL receiver observer'", 0},
			{"SET synchronous_commit=off", 0},
			{"SET synchronous_commit=on", 0},
			{"BEGIN", 0},
			{"UPDATE wal_receiver_sentinel SET v=41", 1},
			{"ROLLBACK", 0},
		} {
			statsExec(t, p, b, peer, command.query, command.count)
			walReceiverEmpty(t, p, b, c)
			walReceiverEmpty(t, p, b, held)
			if command.query != "CREATE TABLE wal_receiver_sentinel(v integer)" {
				walReceiverSentinel(t, p, b, c)
			}
		}
		walReceiverSentinel(t, p, b, c)
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, executor := range []fixture.Executor{c, held} {
				rows, err := executor.QueryContext(ctx, walReceiverSQL)
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled query returned rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary context cancellation", err)
				}
			}
			walReceiverEmpty(t, p, b, held)
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "pg_stat_wal_receiver") {
				t.Fatal("Dump serialized monitoring relation", dump.String())
			}
			for _, restore := range []bool{false, true} {
				retained, err := held.QueryContext(p.Ctx, walReceiverSQL)
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
				walReceiverFinishEmpty(t, retained, false, walReceiverAllColumns())
				// Existing statement revalidation selects the newly owned view;
				// the previous Rows retain their own immutable descriptor above.
				walReceiverEmpty(t, p, b, held)
				walReceiverEmpty(t, p, b, c)
				if restore {
					walReceiverSentinel(t, p, b, c)
				} else {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_class WHERE relname='wal_receiver_sentinel' AND relnamespace='public'::regnamespace", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
			}
		}
	})
}
