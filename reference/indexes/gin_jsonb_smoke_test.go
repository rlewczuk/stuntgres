package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// TestReferenceGINJSONBFeatureSmoke is the shipped jsonb GIN path: create a
// default jsonb_ops index, write after creation, update, dump/restore natively
// and keep the same scalar predicate bags in both modes.
func TestReferenceGINJSONBFeatureSmoke(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE gin_jsonb_smoke(id integer,labels jsonb NOT NULL DEFAULT '{}')", 0))
		run(fixture.Exec("CREATE INDEX gin_jsonb_smoke_i ON gin_jsonb_smoke USING GIN (labels)", 0))
		run(fixture.Exec(`INSERT INTO gin_jsonb_smoke VALUES(1,'{"a":"b"}')`, 1))
		type query struct {
			direct string
			held   fixture.HeldStatement
			arg    any
		}
		queries := []query{
			{`labels @> '{"a":"b"}'`, classHeld(t, p, conn, "SELECT id FROM gin_jsonb_smoke WHERE labels @> $1::jsonb ORDER BY id"), `{"a":"b"}`},
			{`labels ? 'n'`, classHeld(t, p, conn, "SELECT id FROM gin_jsonb_smoke WHERE labels ? $1::text ORDER BY id"), "n"},
			{`labels @> '{"n":1}'`, classHeld(t, p, conn, "SELECT id FROM gin_jsonb_smoke WHERE labels @> $1::jsonb ORDER BY id"), `{"n":1}`},
			{`labels ?& '{a,n}'::text[]`, classHeld(t, p, conn, "SELECT id FROM gin_jsonb_smoke WHERE labels ?& $1::text[] ORDER BY id"), "{a,n}"},
			{`labels ? 'x'`, classHeld(t, p, conn, "SELECT id FROM gin_jsonb_smoke WHERE labels ? $1::text ORDER BY id"), "x"},
		}
		check := func(which int, ids []int64) {
			t.Helper()
			q := queries[which]
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_jsonb_smoke WHERE "+q.direct+" ORDER BY id", ids))
			fixture.RunStep(t, p.Ctx, q.held, b.Recorder, ginIDs(q.held.SQL, ids, q.arg))
		}
		check(0, []int64{1})
		run(fixture.Prepared(fixture.Exec("INSERT INTO gin_jsonb_smoke VALUES($1,$2::jsonb)", 1, 2, `{"a":"c","n":1.0}`)))
		check(1, []int64{2})
		check(2, []int64{2})
		check(3, []int64{2})
		run(fixture.Exec(`UPDATE gin_jsonb_smoke SET labels='{"x":1}' WHERE id=1`, 1))
		check(0, nil)
		check(4, []int64{1})
		metadata := func() {
			t.Helper()
			run(fixture.Select("SELECT indexdef FROM pg_indexes WHERE tablename='gin_jsonb_smoke' AND indexname='gin_jsonb_smoke_i'", []string{"indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX gin_jsonb_smoke_i ON public.gin_jsonb_smoke USING gin (labels)"}}))
			run(fixture.Select("SELECT indclass::text FROM pg_index WHERE indexrelid='gin_jsonb_smoke_i'::regclass", []string{"indclass"}, []string{"TEXT"}, [][]any{{"10090"}}))
		}
		metadata()
		if !b.PG {
			dump := fixture.CheckDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check(0, nil)
			check(2, []int64{2})
			check(4, []int64{1})
			metadata()
		}
		run(fixture.Prepared(fixture.Exec("INSERT INTO gin_jsonb_smoke VALUES($1,$2::jsonb)", 1, 3, `{"x":2}`)))
		check(4, []int64{1, 3})
	})
}