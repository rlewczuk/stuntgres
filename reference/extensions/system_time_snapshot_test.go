package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSystemTimeSnapshot(t *testing.T) {
	stCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer peer.Close()
		all := "SELECT id,payload FROM sr TABLESAMPLE system_time('Infinity') ORDER BY id"
		finite := "SELECT id,payload FROM sr TABLESAMPLE system_time(0.25)"
		changed := [][]any{{int64(1),"v1"},{int64(3),"changed"},{int64(4),"v4"},{int64(5),"v5"},{int64(6),"v6"},{int64(7),"v7"},{int64(8),"v8"},{int64(9),"v9"},{int64(10),"v10"},{int64(11),"private"}}
		mutate := func(tx *sql.Tx) {
			srRun(t, p, b, tx, fixture.Exec("DELETE FROM sr WHERE id=2", 1))
			srRun(t, p, b, tx, fixture.Exec("UPDATE sr SET payload='changed' WHERE id=3", 1))
			srRun(t, p, b, tx, fixture.Exec("INSERT INTO sr VALUES(11,'private')", 1))
		}
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil { t.Fatal(err) }
		defer tx.Rollback()
		mutate(tx)
		srBoth(t, p, b, tx, srMetadata(fixture.Select(all, srNames, srTypes, changed), b.PG))
		stSample(t, p.Ctx, tx, finite, changed)
		srBoth(t, p, b, peer, fixture.Select(all, srNames, srTypes, srRows()))
		stSampleBoth(t, p, peer, finite, srRows())
		if err := tx.Rollback(); err != nil { t.Fatal(err) }
		srBoth(t, p, b, c, fixture.Select(all, srNames, srTypes, srRows()))

		horizon, err := c.BeginTx(p.Ctx, &sql.TxOptions{Isolation:sql.LevelRepeatableRead})
		if err != nil { t.Fatal(err) }
		defer horizon.Rollback()
		// A real read establishes the horizon before the peer publishes DML.
		srRun(t, p, b, horizon, srCount("SELECT count(*) AS n FROM sr", 10))
		writer, err := peer.BeginTx(p.Ctx, nil)
		if err != nil { t.Fatal(err) }
		defer writer.Rollback()
		mutate(writer)
		if err := writer.Commit(); err != nil { t.Fatal(err) }
		srBoth(t, p, b, horizon, fixture.Select(all, srNames, srTypes, srRows()))
		stSample(t, p.Ctx, horizon, finite, srRows())
		h, err := horizon.PrepareContext(p.Ctx, finite)
		if err != nil { t.Fatal(err) }
		stSample(t, p.Ctx, fixture.HeldStatement{Executor:horizon, Statement:h, SQL:finite}, finite, srRows())
		if err := h.Close(); err != nil { t.Fatal(err) }
		if err := horizon.Rollback(); err != nil { t.Fatal(err) }
		srBoth(t, p, b, c, fixture.Select(all, srNames, srTypes, changed))
		stSampleBoth(t, p, c, finite, changed)

		// Duplicate source occurrences are legal; payload NULL and duplicate IDs
		// do not authorize the sampler to invent or duplicate an occurrence.
		srRun(t, p, b, c, fixture.Exec("INSERT INTO sr VALUES(1,'v1'),(1,NULL)", 2))
		duplicates := append(changed, []any{int64(1),"v1"}, []any{int64(1),nil})
		stSampleBoth(t, p, c, finite, duplicates)
		s := fixture.Select("SELECT id,payload FROM sr TABLESAMPLE system_time('Infinity')", srNames, srTypes, duplicates)
		s.Bag = true
		srBoth(t, p, b, c, s)
	})
}
