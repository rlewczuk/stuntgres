package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferenceGINJSONBPredicates pins the jsonb GIN predicate bags. Every
// expectation is independent of native output; the scalar jsonb/jsonpath
// matcher remains the SQL authority and the index only changes candidate work.
func TestReferenceGINJSONBPredicates(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE pj(id integer,labels jsonb)", 0))
		run(fixture.Exec(`INSERT INTO pj VALUES
			(1,'{"a":"b","n":1,"arr":[1,2],"o":{"k":"v"}}'::jsonb),
			(2,'{"a":"c","n":1.0,"arr":[3]}'::jsonb),
			(3,'{}'::jsonb),
			(4,NULL),
			(5,'null'::jsonb),
			(6,'[1,2,3]'::jsonb),
			(7,'["a","b"]'::jsonb),
			(8,'{"a":null}'::jsonb),
			(9,'{"x":{"a":1}}'::jsonb),
			(10,'{"a":{"b":1}}'::jsonb)`, 10))
		cases := []struct {
			direct, prepared string
			arg              any
			ids              []int64
		}{
			{`labels @> '{"a":"b"}'::jsonb`, `labels @> $1::jsonb`, `{"a":"b"}`, []int64{1}},
			{`labels @> '{"n":1}'::jsonb`, `labels @> $1::jsonb`, `{"n":1}`, []int64{1, 2}},
			{`labels @> '{"a":null}'::jsonb`, `labels @> $1::jsonb`, `{"a":null}`, []int64{8}},
			{`labels @> '{}'::jsonb`, `labels @> $1::jsonb`, `{}`, []int64{1, 2, 3, 8, 9, 10}},
			{`labels @> '[3]'::jsonb`, `labels @> $1::jsonb`, `[3]`, []int64{6}},
			{`labels @> '{"o":{"k":"v"}}'::jsonb`, `labels @> $1::jsonb`, `{"o":{"k":"v"}}`, []int64{1}},
			{`labels ? 'a'`, `labels ? $1::text`, `a`, []int64{1, 2, 7, 8, 10}},
			{`labels ? 'b'`, `labels ? $1::text`, `b`, []int64{7}},
			{`labels ? 'k'`, `labels ? $1::text`, `k`, nil},
			{`labels ? 'n'`, `labels ? $1::text`, `n`, []int64{1, 2}},
			{`labels ? 'q'`, `labels ? $1::text`, `q`, nil},
			{`labels ?| '{a,zzz}'::text[]`, `labels ?| $1::text[]`, `{a,zzz}`, []int64{1, 2, 7, 8, 10}},
			{`labels ?| '{}'::text[]`, `labels ?| $1::text[]`, `{}`, nil},
			{`labels ?& '{a,n}'::text[]`, `labels ?& $1::text[]`, `{a,n}`, []int64{1, 2}},
			{`labels ?& '{a,b}'::text[]`, `labels ?& $1::text[]`, `{a,b}`, []int64{7}},
			{`labels ?& '{}'::text[]`, `labels ?& $1::text[]`, `{}`, []int64{1, 2, 3, 5, 6, 7, 8, 9, 10}},
			{`labels ?& '{a,q}'::text[]`, `labels ?& $1::text[]`, `{a,q}`, nil},
			{`labels ? NULL::text`, `labels ? $1::text`, nil, nil},
			{`labels @? '$.a'::jsonpath`, `labels @? $1::jsonpath`, `$.a`, []int64{1, 2, 8, 10}},
			{`labels @@ '$.n == 1'::jsonpath`, `labels @@ $1::jsonpath`, `$.n == 1`, []int64{1, 2}},
			{`labels @@ '$.a == "b"'::jsonpath`, `labels @@ $1::jsonpath`, `$.a == "b"`, []int64{1}},
		}
		held := make([]fixture.HeldStatement, len(cases))
		for i, tc := range cases {
			held[i] = classHeld(t, p, conn, "SELECT id FROM pj WHERE "+tc.prepared+" ORDER BY id")
		}
		checkOne := func(i int, ids []int64) {
			t.Helper()
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM pj WHERE "+cases[i].direct+" ORDER BY id", ids))
			fixture.RunStep(t, p.Ctx, held[i], b.Recorder, ginIDs(held[i].SQL, ids, cases[i].arg))
		}
		check := func() {
			t.Helper()
			for i := range cases {
				checkOne(i, cases[i].ids)
			}
		}
		check()
		run(fixture.Exec("CREATE INDEX pj_def ON pj USING GIN (labels)", 0))
		check()
		run(fixture.Exec("CREATE INDEX pj_exp ON pj USING GIN (labels pg_catalog.jsonb_ops)", 0))
		check()
		run(fixture.Exec("DROP INDEX pj_exp", 0))
		run(fixture.Exec("CREATE INDEX pj_path ON pj USING GIN (labels jsonb_path_ops)", 0))
		// jsonb_path_ops covers @> and the jsonpath strategies only; the ?
		// family must keep the same heap truth with a non-covering class.
		check()
		run(fixture.Exec("DROP INDEX pj_path", 0))

		run(fixture.Prepared(fixture.Exec("INSERT INTO pj VALUES($1,$2::jsonb)", 1, 11, `{"a":"b","q":[true]}`)))
		checkOne(0, []int64{1, 11})
		checkOne(10, []int64{11})
		checkOne(16, []int64{11})
		run(fixture.Exec(`UPDATE pj SET labels='{"a":"z"}' WHERE id=1`, 1))
		checkOne(0, []int64{11})
		checkOne(6, []int64{1, 2, 7, 8, 10, 11})
		run(fixture.Exec("DELETE FROM pj WHERE id=2", 1))
		checkOne(9, nil)
		run(fixture.Exec(`INSERT INTO pj VALUES(12,'{"a":"b"}'),(12,'{"a":"b"}')`, 2))
		checkOne(0, []int64{11, 12, 12})
	})
}