package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLPreparedRetainedRowsAfterEnd(t *testing.T) {
	sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE retained AS SELECT 7 AS value", 0))
		q := "EXECUTE retained"
		if b.PG {
			// A pq cursor owns live transport until consumed; detached native eager
			// rows are a public native contract, not a wire-emulation promise.
			sqlPreparedStep(t, p, b, c, sqlPreparedScalar(q, "value", "INT4", int64(7)))
			return
		}
		ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
		rows, err := c.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		frozen := fixture.SequenceFrozenRows{Executor: c, Rows: rows, Query: q}
		sqlPreparedStep(t, p, b, c, fixture.Exec("DEALLOCATE retained", 0))
		// Close the actual public driver connection while leaving database/sql's
		// borrowed wrapper intact until its eager rows have been consumed.
		// Raw ErrBadConn instead makes sql.Conn.close wait for these Rows and
		// eventually cancels their query context, which is not engine lifetime.
		if err := c.Raw(func(raw any) error { return raw.(driver.Conn).Close() }); err != nil {
			t.Fatal(err)
		}
		sqlPreparedStep(t, p, b, frozen, sqlPreparedScalar(q, "value", "INT4", int64(7)))
	})
}
