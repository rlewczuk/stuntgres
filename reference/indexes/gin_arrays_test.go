package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGINArrays(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE gin_arrays(id integer,a integer[])", 0))
		run(fixture.Exec(`INSERT INTO gin_arrays VALUES(1,'{1,2,2}'),(2,'{2,3}'),(3,'{}'),(4,NULL),(5,'{NULL,2}'),(6,'[0:1]={1,2}'),(7,'{1,2}'),(8,'{{1,2},{3,4}}')`, 8))
		cases := []struct {
			op, rhs string
			ids     []int64
		}{
			{"&&", "{2}", []int64{1, 2, 5, 6, 7, 8}},
			{"@>", "{1,2}", []int64{1, 6, 7, 8}},
			{"<@", "{1,2}", []int64{1, 3, 6, 7}},
			{"=", "{1,2}", []int64{7}},
			{"@>", "{}", []int64{1, 2, 3, 5, 6, 7, 8}},
			{"<@", "{}", []int64{3}},
			{"&&", "{}", nil},
			{"=", "{NULL,2}", []int64{5}},
			{"@>", "{NULL}", nil},
			{"&&", "{NULL}", nil},
			{"@>", "{2,2,2}", []int64{1, 2, 5, 6, 7, 8}},
		}
		held := make([]fixture.HeldStatement, len(cases))
		for i, tc := range cases {
			held[i] = classHeld(t, p, conn, "SELECT id FROM gin_arrays WHERE a "+tc.op+" $1::integer[] ORDER BY id")
		}
		check := func() {
			for i, tc := range cases {
				s := ginIDs(held[i].SQL, tc.ids, tc.rhs)
				classRoutes(t, p, b, conn, s)
				fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
			}
			classRoutes(t, p, b, conn, fixture.Select("SELECT a && NULL::integer[] AS overlap,a @> NULL::integer[] AS contains,NULL::integer[] <@ a AS contained FROM gin_arrays ORDER BY id", []string{"overlap", "contains", "contained"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{nil, nil, nil}, {nil, nil, nil}, {nil, nil, nil}, {nil, nil, nil}, {nil, nil, nil}, {nil, nil, nil}, {nil, nil, nil}, {nil, nil, nil}}))
		}
		check() // Scalar authority before any index exists.
		for _, definition := range []string{"a", "a array_ops", "a pg_catalog.array_ops"} {
			run(fixture.Exec("CREATE INDEX gin_arrays_i ON gin_arrays USING gin("+definition+")", 0))
			check()
			run(fixture.Exec("DROP INDEX gin_arrays_i", 0))
		}
		run(fixture.Exec("CREATE INDEX gin_arrays_i ON gin_arrays USING gin(a)", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO gin_arrays VALUES($1,$2::integer[])", 1, 9, "{2,2}")))
		classRoutes(t, p, b, conn, ginIDs(held[0].SQL, []int64{1, 2, 5, 6, 7, 8, 9}, "{2}"))
		fixture.RunStep(t, p.Ctx, held[0], b.Recorder, ginIDs(held[0].SQL, []int64{1, 2, 5, 6, 7, 8, 9}, "{2}"))
		classRoutes(t, p, b, conn, ginIDs(held[2].SQL, []int64{1, 3, 6, 7, 9}, "{1,2}"))
		classRoutes(t, p, b, conn, ginIDs(held[3].SQL, nil, "{2}"))
		run(fixture.Exec("UPDATE gin_arrays SET a='{9}' WHERE id=1", 1))
		run(fixture.Exec("DELETE FROM gin_arrays WHERE id=2", 1))
		fixture.RunStep(t, p.Ctx, held[0], b.Recorder, ginIDs(held[0].SQL, []int64{5, 6, 7, 8, 9}, "{2}"))
		run(fixture.Exec("INSERT INTO gin_arrays VALUES(10,'{2}'),(10,'{2}')", 2))
		classRoutes(t, p, b, conn, ginIDs(held[3].SQL, []int64{10, 10}, "{2}"))

		run(fixture.Exec("CREATE TABLE gin_text_arrays(id integer,a text[],b text[],v tsvector)", 0))
		run(fixture.Exec("CREATE INDEX gin_text_arrays_i ON gin_text_arrays USING gin(a,b,v,a)", 0))
		run(fixture.Exec(`INSERT INTO gin_text_arrays VALUES(1,'{cat,cat}','{dog}','rat:1'),(2,'{dog}','{cat}','cat:1'),(3,'{}','{}',''),(4,NULL,NULL,NULL)`, 4))
		classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_text_arrays WHERE a @> $1::text[] ORDER BY id", []int64{1}, "{cat}"))
		classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_text_arrays WHERE b && $1::text[] AND v @@ 'cat'::tsquery ORDER BY id", []int64{2}, "{cat}"))
		classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_text_arrays WHERE a = '{}'::text[] AND b <@ '{}'::text[] ORDER BY id", []int64{3}))
	})
}
