package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceSystemTimeElapsed(t *testing.T) {
	stCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stElapsed(t, p, b, c)
	})
}

func stElapsed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	srRun(t, p, b, c, fixture.Exec("CREATE TABLE st_large(id integer NOT NULL,payload text)", 0))
	const size, batch = 20000, 500
	visible := make([][]any, 0, size)
	for start := 1; start <= size; start += batch {
		var q strings.Builder
		q.WriteString("INSERT INTO st_large VALUES")
		for id := start; id < start+batch; id++ {
			if id != start { q.WriteByte(',') }
			fmt.Fprintf(&q, "(%d,'row%d')", id, id)
			visible = append(visible, []any{int64(id),fmt.Sprintf("row%d",id)})
		}
		srRun(t, p, b, c, fixture.Exec(q.String(), batch))
	}
	q := "SELECT id,payload FROM st_large TABLESAMPLE system_time($1)"
	h := srHeld(t, p, c, q)
	for _, executor := range []fixture.Executor{c,h} {
		// This is a generous whole-query liveness guard, not a claim that
		// preparation, sorting or client consumption fits the sample budget.
		ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
		stSample(t, ctx, executor, q, visible, 0.25)
		cancel()
	}
	srBoth(t, p, b, c, srCount("SELECT count(*) AS n FROM st_large TABLESAMPLE system_time('Infinity')", size))
	stExclusive(t, p, b, "st_large")
}

func stExclusive(t *testing.T, p *fixture.Pair, b fixture.Backend, table string) {
	t.Helper()
	peer, err := b.DB.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer peer.Close()
	tx, err := peer.BeginTx(p.Ctx, nil)
	if err != nil { t.Fatal(err) }
	defer tx.Rollback()
	srRun(t, p, b, tx, fixture.Exec("LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
	if err := tx.Rollback(); err != nil { t.Fatal(err) }
}
