package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatUserIndexesReaderConsistency(t *testing.T) {
	for _, mode := range []string{"cache", "snapshot", "none"} {
		t.Run(mode, func(t *testing.T) {
			fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				name := "uix_reader_"+mode
				sysIndexCreate(t, p, b, c, "public", name)
				marker := name+"_publication"
				fixture.StatisticsCreateTables(t, p, b, c, marker)
				counts := [3]int64{}
				publish := func(phase int64) {
					t.Helper()
					w, retire := fixture.StatisticsReportingConn(t, p, b)
					indexStatPrefer(t, p, b, w)
					q := "SELECT payload FROM public."+name+" WHERE n=$1"
					delta := indexStatDelta(t, b, indexStatPlan(t, p, w, q, 2), 1, 2)
					indexStatPayloads(t, p, b, w, q, []string{"a", "b"}, 2)
					fixture.StatisticsExec(t, p, b, w, "INSERT INTO "+marker+" VALUES(1)", 1)
					retire()
					fixture.StatisticsAwaitTableCount(t, p, b, marker, "n_tup_ins", phase)
					for i := range counts {
						counts[i] += delta[i]
					}
					indexStatAwait(t, p, b, name+"_hash", counts[0], counts[1], counts[2])
				}
				publish(1)
				fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency="+mode, 0)
				stmt, err := c.PrepareContext(p.Ctx, userIndexSelect)
				if err != nil { t.Fatal(err) }
				defer stmt.Close()
				fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
				defer c.ExecContext(context.Background(), "ROLLBACK")
				var timestamp sql.NullTime
				if err := c.QueryRowContext(p.Ctx, "SELECT pg_stat_get_snapshot_timestamp()").Scan(&timestamp); err != nil || timestamp.Valid {
					t.Fatal("no admitted demand must mean no snapshot", timestamp, err)
				}
				old, err := userIndexHeld(t, p, stmt, name+"_hash")
				if err != nil { t.Fatal(err) }
				indexStatWant(t, old, counts[0], counts[1], counts[2])
				if !indexStatGet(t, p, c, old.Name).equal(old) { t.Fatal("initial shared cut") }
				publish(2)
				current, err := userIndexHeld(t, p, stmt, old.Name)
				if err != nil { t.Fatal(err) }
				if mode == "none" {
					indexStatWant(t, current, counts[0], counts[1], counts[2])
				} else if !current.equal(old) { t.Fatal("shared Reader cut not retained", old, current) }
				if !indexStatGet(t, p, c, old.Name).equal(current) { t.Fatal("ALL has a different Reader cut", current) }
				fixture.StatisticsClearSnapshot(t, p, b, c)
				fresh, err := userIndexHeld(t, p, stmt, old.Name)
				if err != nil { t.Fatal(err) }
				indexStatWant(t, fresh, counts[0], counts[1], counts[2])
				if !indexStatGet(t, p, c, old.Name).equal(fresh) { t.Fatal("clear failed to refresh shared Reader") }
				if !b.PG { indexStatWant(t, old, 1, 2, 2); indexStatWant(t, fresh, 2, 4, 4) }
				fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
			})
		})
	}
}
