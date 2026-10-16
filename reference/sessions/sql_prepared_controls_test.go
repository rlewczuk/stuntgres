package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLPreparedNativeControls(t *testing.T) {
	for _, replacement := range []string{"reset", "restore", "roundtrip"} {
		t.Run(replacement, func(t *testing.T) {
			sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqlPreparedStep(t, p, b, c, fixture.Exec("CREATE TABLE prep_control(v int)", 0))
				sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE control_ins(int) AS INSERT INTO prep_control VALUES($1)", 0))
				sqlPreparedStep(t, p, b, c, fixture.Exec("EXECUTE control_ins(7)", 1))
				sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE constant AS SELECT 11", 0))
				if b.PG {
					// Native host replacement has no PostgreSQL SQL counterpart.
					sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT v FROM prep_control", "v", "INT4", int64(7)))
					return
				}
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				sqlPreparedStep(t, p, b, peer, fixture.Exec("PREPARE constant AS SELECT 22", 0))
				defer peer.ExecContext(p.Ctx, "DEALLOCATE ALL")
				q := "EXECUTE constant"
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
				rows, err := c.QueryContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				frozen := fixture.SequenceFrozenRows{Executor: c, Rows: rows, Query: q}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(dump.String(), "PREPARE") || strings.Contains(dump.String(), "control_ins") || strings.Contains(dump.String(), "constant") {
					t.Fatalf("Dump leaked definitions: %s", dump.String())
				}
				switch replacement {
				case "reset":
					err = stuntgres.Reset(p.Ctx, p.Native)
				case "restore":
					err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE prep_control(v int); INSERT INTO prep_control VALUES(99)"))
				case "roundtrip":
					err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
				}
				if err != nil {
					t.Fatal(err)
				}
				sqlPreparedStep(t, p, b, c, fixture.Exec("DEALLOCATE constant", 0))
				sqlPreparedStep(t, p, b, frozen, sqlPreparedScalar(q, "?column?", "INT4", int64(11)))
				sqlPreparedStep(t, p, b, peer, sqlPreparedScalar(q, "?column?", "INT4", int64(22)))
				if replacement == "reset" {
					sqlPreparedStep(t, p, b, c, fixture.Exec("CREATE TABLE prep_control(v int)", 0))
				}
				sqlPreparedStep(t, p, b, c, fixture.Exec("EXECUTE control_ins(8)", 1))
				want := [][]any{{int64(8)}}
				if replacement == "restore" {
					want = [][]any{{int64(8)}, {int64(99)}}
				}
				if replacement == "roundtrip" {
					want = [][]any{{int64(7)}, {int64(8)}}
				}
				sqlPreparedStep(t, p, b, c, fixture.Select("SELECT v FROM prep_control ORDER BY v", []string{"v"}, []string{"INT4"}, want))
			})
		})
	}
}

func TestReferenceSQLPreparedRestoreExclusions(t *testing.T) {
	for _, command := range []string{"PREPARE p AS SELECT 1", "EXECUTE p", "DEALLOCATE ALL"} {
		t.Run(command, func(t *testing.T) {
			sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqlPreparedStep(t, p, b, c, fixture.Exec("CREATE TABLE prep_restore(v int)", 0))
				sqlPreparedStep(t, p, b, c, fixture.Exec("INSERT INTO prep_restore VALUES(7)", 1))
				if b.PG {
					return
				}
				err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE partial(v int); INSERT INTO partial VALUES(9); "+command))
				fixture.State(t, err, "0A000")
				sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT v FROM prep_restore", "v", "INT4", int64(7)))
				sqlPreparedStep(t, p, b, c, fixture.ErrorDiagnostic("EXECUTE p", sqlPreparedMissing("p")))
			})
		})
	}
}

func TestReferenceSQLPreparedCanceledWrite(t *testing.T) {
	sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlPreparedStep(t, p, b, c, fixture.Exec("CREATE TABLE prep_cancel(v int)", 0))
		sqlPreparedStep(t, p, b, c, fixture.Exec("INSERT INTO prep_cancel VALUES(7)", 1))
		if b.PG {
			// Observe a genuinely running prepared write before admin cancellation;
			// pg_sleep is only a pinned-oracle admission witness, not native scope.
			sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE slow AS INSERT INTO prep_cancel SELECT v FROM (VALUES(8),(9)) s(v) WHERE pg_sleep(600) IS NULL RETURNING v", 0))
			fixture.SQLCursorCancelDemand(t, p, c, "EXECUTE slow")
		} else {
			// pg_sleep has no native provider; retain the genuine precanceled route.
			sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE slow(int) AS INSERT INTO prep_cancel VALUES($1),($1+1) RETURNING v", 0))
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := c.QueryContext(ctx, "EXECUTE slow(8)")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled write exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT v FROM prep_cancel", "v", "INT4", int64(7)))
	})
}
