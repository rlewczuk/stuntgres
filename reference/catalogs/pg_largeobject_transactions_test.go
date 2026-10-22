package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGLargeobjectTransactions(t *testing.T) {
	pglScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		other := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, pglBackendStep(s, b)) }
		run(pglCreate(920101, "aa00"))
		run(fixture.Exec("BEGIN", 0))
		run(pglPut(920101, 0, "bbff"))
		run(pglCreate(920102, "0080ff"))
		run(pglPages(920101, 0, []byte{187, 255}))
		run(pglPages(920102, 0, []byte{0, 128, 255}))
		other(pglPages(920101, 0, []byte{170, 0}))
		other(pglPages(920102))
		run(fixture.Exec("SAVEPOINT pgl_s", 0))
		run(pglPut(920101, 4097, "cc"))
		run(pglValue("lo_unlink(920102::oid)", "INT4", int64(1)))
		run(pglPages(920101, 0, []byte{187, 255}, 2, []byte{0, 204}))
		run(pglPages(920102))
		run(fixture.Exec("ROLLBACK TO pgl_s", 0))
		run(pglPages(920101, 0, []byte{187, 255}))
		run(pglPages(920102, 0, []byte{0, 128, 255}))
		run(fixture.Exec("RELEASE pgl_s", 0))
		run(fixture.Exec("COMMIT", 0))
		other(pglPages(920101, 0, []byte{187, 255}))
		other(pglPages(920102, 0, []byte{0, 128, 255}))
		run(fixture.Exec("BEGIN", 0))
		run(pglPut(920101, 0, "ccdd"))
		run(pglCreate(920103, "ff"))
		run(pglValue("lo_unlink(920102::oid)", "INT4", int64(1)))
		other(pglPages(920101, 0, []byte{187, 255}))
		other(pglPages(920103))
		other(pglPages(920102, 0, []byte{0, 128, 255}))
		run(fixture.Exec("ROLLBACK", 0))
		run(pglPages(920101, 0, []byte{187, 255}))
		run(pglPages(920103))
		run(pglPages(920102, 0, []byte{0, 128, 255}))
		run(fixture.Exec("BEGIN", 0))
		run(pglValue("lo_unlink(920102::oid)", "INT4", int64(1)))
		run(pglPages(920102))
		other(pglPages(920102, 0, []byte{0, 128, 255}))
		run(fixture.Exec("COMMIT", 0))
		other(pglPages(920102))
		// The late outer read fails after a demanded write. No candidate escapes.
		run(pglError("SELECT lo_put(920101::oid,0::bigint,decode('eeee','hex')),lo_get(920120::oid)", "42704", "large object 920120 does not exist"))
		run(pglPages(920101, 0, []byte{187, 255}))
		other(pglPages(920101, 0, []byte{187, 255}))
	})
}

func TestReferencePGLargeobjectPrepared(t *testing.T) {
	pglScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := c.QueryContext(ctx, "SELECT loid,pageno,data FROM pg_largeobject WHERE loid=920101::oid")
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled query published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled query: %T %v", err, err)
			}
			run(pglPages(920101))
		}
		q := "SELECT loid,pageno,data FROM pg_largeobject WHERE loid=$1 ORDER BY pageno"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		run(pglPages(920101))
		run(pglCreate(920101, "0080ff"))
		run(pglCreate(920102, "6162"))
		check := func(oid int, data []byte) {
			s := pglPages(oid, 0, data)
			s.Query = q
			s.Args = []any{int64(oid)}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, pglBackendStep(s, b))
		}
		check(920101, []byte{0, 128, 255})
		check(920102, []byte("ab"))
		rows, err := held.QueryContext(p.Ctx, int64(920101))
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		var oid int64
		var page int
		var saved []byte
		if !rows.Next() {
			rows.Close()
			t.Fatal("missing held row", rows.Err())
		}
		if err := rows.Scan(&oid, &page, &saved); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		run(pglPut(920101, 0, "aabbcc"))
		check(920101, []byte{170, 187, 204})
		run(pglValue("lo_unlink(920101::oid)", "INT4", int64(1)))
		s := pglPages(920101)
		s.Query = q
		s.Args = []any{int64(920101)}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, pglBackendStep(s, b))
		if oid != 920101 || page != 0 || !bytes.Equal(saved, []byte{0, 128, 255}) {
			t.Fatal("retained bytes changed", oid, page, saved)
		}
		pglMetadata(t, types, b.PG)
	})
}
