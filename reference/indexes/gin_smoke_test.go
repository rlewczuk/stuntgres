package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceGINFeatureSmoke(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE gin_smoke(id integer,a integer[],v tsvector)", 0))
		run(fixture.Exec("CREATE INDEX gin_smoke_a ON gin_smoke USING gin(a)", 0))
		run(fixture.Prepared(fixture.Exec("CREATE INDEX gin_smoke_v ON gin_smoke USING gin(v pg_catalog.tsvector_ops)", 0)))
		run(fixture.Exec("INSERT INTO gin_smoke VALUES(1,'{1,2,2}','cat:1 rat:2'),(2,'{}','dog:1'),(3,NULL,NULL)", 3))
		array := classHeld(t, p, conn, "SELECT id FROM gin_smoke WHERE a @> $1::integer[] ORDER BY id")
		vector := classHeld(t, p, conn, "SELECT id FROM gin_smoke WHERE v @@ $1::tsquery ORDER BY id")
		check := func(ids []int64) {
			for _, tc := range []struct {
				held fixture.HeldStatement
				arg  string
			}{{array, "{2}"}, {vector, "cat <-> rat"}} {
				s := ginIDs(tc.held.SQL, ids, tc.arg)
				classRoutes(t, p, b, conn, s)
				fixture.RunStep(t, p.Ctx, tc.held, b.Recorder, s)
			}
		}
		check([]int64{1})
		run(fixture.Exec("UPDATE gin_smoke SET a='{9}',v='dog:1' WHERE id=1", 1))
		check(nil)
		if !b.PG {
			dump := fixture.CheckDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check(nil)
		}
		run(fixture.Prepared(fixture.Exec("INSERT INTO gin_smoke VALUES($1,$2::integer[],$3::tsvector)", 1, 4, "{2,2}", "cat:1 rat:2")))
		check([]int64{4})
	})
}
