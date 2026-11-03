package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatsXactBlocksSmoke(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, peer *sql.Conn) {
		statsCreate(t, p, b, peer, "xact_smoke_a", "xact_smoke_b")
		statsExec(t, p, b, peer, "INSERT INTO xact_smoke_a VALUES(1),(2)", 2)
		statsExec(t, p, b, peer, "INSERT INTO xact_smoke_b VALUES(9)", 1)
		a, other := xactBlockOID(t, p, peer, "xact_smoke_a"), xactBlockOID(t, p, peer, "xact_smoke_b")
		c, retire := statsReportingConn(t, p, b)
		defer retire()
		getter, err := c.PrepareContext(p.Ctx, xactBlockSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer getter.Close()
		const readSQL = "SELECT id FROM xact_smoke_a ORDER BY id"
		reader, err := c.PrepareContext(p.Ctx, readSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		xactBlockBegin(t, p, b, c)
		defer c.ExecContext(context.Background(), "ROLLBACK")
		statsExec(t, p, b, c, "SET LOCAL stats_fetch_consistency=snapshot", 0)
		xactBlockWant(t, xactBlockRead(t, p, c, getter, 0), xactBlockPair{})
		heldGetter := fixture.HeldStatement{Executor: c, Statement: getter, SQL: xactBlockSQL}
		fixture.RunStep(t, p.Ctx, heldGetter, b.Recorder, fixture.Select(xactBlockSQL, xactBlockNames, []string{"INT8", "INT8"}, [][]any{{nil, nil}}, nil))
		base, untouched := xactBlockRead(t, p, c, getter, a), xactBlockRead(t, p, c, getter, other)
		statsNoTimestamp(t, p, c, nil)
		xactBlockRows(t, p, b, c, readSQL, 1, 2)
		first := xactBlockRead(t, p, c, getter, a)
		xactBlockAdvanced(t, b, base, first)
		xactBlockWant(t, xactBlockRead(t, p, c, getter, other), untouched)
		heldRead := fixture.HeldStatement{Executor: c, Statement: reader, SQL: readSQL}
		fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, fixture.Select(readSQL, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		second := xactBlockRead(t, p, c, getter, a)
		xactBlockAdvanced(t, b, first, second)
		statsNoTimestamp(t, p, c, nil)
		statsClear(t, p, b, c)
		xactBlockWant(t, xactBlockRead(t, p, c, getter, a), second)
		statsNoTimestamp(t, p, c, nil)
		statsExec(t, p, b, c, "SET LOCAL track_counts=off", 0)
		xactBlockRows(t, p, b, c, readSQL, 1, 2)
		xactBlockWant(t, xactBlockRead(t, p, c, getter, a), second)
		statsExec(t, p, b, c, "SET LOCAL track_counts=on", 0)
		xactBlockRows(t, p, b, c, readSQL, 1, 2)
		third := xactBlockRead(t, p, c, getter, a)
		xactBlockAdvanced(t, b, second, third)
		xactBlockRows(t, p, b, peer, readSQL, 1, 2)
		xactBlockWant(t, xactBlockRead(t, p, c, getter, a), third)
		xactBlockWant(t, xactBlockRead(t, p, c, getter, other), untouched)
		statsExec(t, p, b, c, "CREATE TEMP TABLE xact_smoke_temp(id integer)", 0)
		statsExec(t, p, b, c, "INSERT INTO xact_smoke_temp VALUES(7)", 1)
		temp := xactBlockOID(t, p, c, "xact_smoke_temp")
		beforeTemp := xactBlockRead(t, p, c, getter, temp)
		xactBlockRows(t, p, b, c, "SELECT id FROM xact_smoke_temp ORDER BY id", 7)
		xactBlockAdvanced(t, b, beforeTemp, xactBlockRead(t, p, c, getter, temp))
		xactBlockWant(t, xactBlockRead(t, p, peer, nil, temp), xactBlockPair{})
		xactBlockTerminal(t, p, b, c, "COMMIT")
		statsExec(t, p, b, c, "BEGIN", 0)
		xactBlockWant(t, xactBlockRead(t, p, c, getter, a), xactBlockPair{})
		xactBlockWant(t, xactBlockRead(t, p, c, getter, other), xactBlockPair{})
		statsExec(t, p, b, c, "ROLLBACK", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT current_setting('track_counts') AS tracking", []string{"tracking"}, []string{"TEXT"}, [][]any{{"on"}}))
		xactBlockRows(t, p, b, peer, readSQL, 1, 2)
	})
}
