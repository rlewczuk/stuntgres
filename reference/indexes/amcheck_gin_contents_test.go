package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAmcheckGINContents(t *testing.T) {
	heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE EXTENSION amcheck",
			"CREATE TABLE gin_am_contents(id integer,a integer[],v tsvector)",
			"CREATE INDEX gin_am_array ON gin_am_contents USING gin(a)",
			"CREATE INDEX gin_am_vector ON gin_am_contents USING gin(v)",
			"CREATE INDEX gin_am_multi ON gin_am_contents USING gin(a,v)",
			"CREATE INDEX gin_am_expr ON gin_am_contents USING gin((COALESCE(a,'{}'::integer[])))",
			"CREATE INDEX gin_am_partial ON gin_am_contents USING gin(a) WHERE id>2",
		} {
			run(fixture.Exec(q, 0))
		}
		indexes := []string{"gin_am_array", "gin_am_vector", "gin_am_multi", "gin_am_expr", "gin_am_partial"}
		check := func() {
			t.Helper()
			for _, name := range indexes {
				heapBoth(t, p, b, c, amcheckSelect("SELECT gin_index_check('"+name+"') AS ok", false))
			}
		}
		check()
		run(fixture.Select("SELECT id,a::text,v::text FROM gin_am_contents ORDER BY id", []string{"id", "a", "v"}, []string{"INT4", "TEXT", "TEXT"}, nil))
		run(fixture.Exec(`INSERT INTO gin_am_contents VALUES
			(1,'{1,1,2}','cat:1 cat:2 dog:3'),
			(2,'{1,1,2}','cat:1 cat:2 dog:3'),
			(3,'{NULL}',''),(4,'{}',NULL),(5,NULL,'cat:1'),
			(6,'[0:1]={2,3}','dog:1'),(7,'{2,NULL,2}','cat:1 dog:2')`, 7))
		initial := [][]any{
			{int64(1), "{1,1,2}", "'cat':1,2 'dog':3"},
			{int64(2), "{1,1,2}", "'cat':1,2 'dog':3"},
			{int64(3), "{NULL}", ""}, {int64(4), "{}", nil},
			{int64(5), nil, "'cat':1"}, {int64(6), "[0:1]={2,3}", "'dog':1"},
			{int64(7), "{2,NULL,2}", "'cat':1 'dog':2"},
		}
		contents := func(rows [][]any) {
			t.Helper()
			heapBoth(t, p, b, c, fixture.Select("SELECT id,a::text,v::text FROM gin_am_contents ORDER BY id", []string{"id", "a", "v"}, []string{"INT4", "TEXT", "TEXT"}, rows))
		}
		contents(initial)
		check()
		// This dictionary is built from existing rows, unlike the five above.
		run(fixture.Exec("CREATE INDEX gin_am_late ON gin_am_contents USING gin(a,v)", 0))
		indexes = append(indexes, "gin_am_late")
		check()
		heapBoth(t, p, b, c, ginIDs("SELECT id FROM gin_am_contents WHERE a @> '{2}'::integer[] ORDER BY id", []int64{1, 2, 6, 7}))
		heapBoth(t, p, b, c, ginIDs("SELECT id FROM gin_am_contents WHERE v @@ 'cat'::tsquery ORDER BY id", []int64{1, 2, 5, 7}))
		heapBoth(t, p, b, c, ginIDs("SELECT id FROM gin_am_contents WHERE id>2 AND a && '{3}'::integer[] ORDER BY id", []int64{6}))
		run(fixture.Exec("INSERT INTO gin_am_contents VALUES(8,'{9,9}','owl:1')", 1))
		contents(append(append([][]any(nil), initial...), []any{int64(8), "{9,9}", "'owl':1"}))
		check()
		run(fixture.Prepared(fixture.Exec("UPDATE gin_am_contents SET a=$1::integer[],v=$2::tsvector WHERE id=8", 1, "{2,3}", "cat:1")))
		contents(append(append([][]any(nil), initial...), []any{int64(8), "{2,3}", "'cat':1"}))
		check()
		run(fixture.Exec("DELETE FROM gin_am_contents WHERE id=8", 1))
		contents(initial)
		check()
		// Keep rollback scoped to this phase on the same physical borrower.
		func() {
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("INSERT INTO gin_am_contents VALUES(8,'{9,9}','owl:1')", 1))
			check()
			run(fixture.Exec("SAVEPOINT gin_am_before", 0))
			run(fixture.Exec("UPDATE gin_am_contents SET a='{8}',v='owl:1' WHERE id=1", 1))
			check()
			run(fixture.Exec("DELETE FROM gin_am_contents WHERE id=2", 1))
			check()
			contents([][]any{
				{int64(1), "{8}", "'owl':1"}, {int64(3), "{NULL}", ""}, {int64(4), "{}", nil},
				{int64(5), nil, "'cat':1"}, {int64(6), "[0:1]={2,3}", "'dog':1"},
				{int64(7), "{2,NULL,2}", "'cat':1 'dog':2"}, {int64(8), "{9,9}", "'owl':1"},
			})
			run(fixture.Exec("ROLLBACK TO gin_am_before", 0))
			contents(append(append([][]any(nil), initial...), []any{int64(8), "{9,9}", "'owl':1"}))
			check()
			// Real candidate roots may already include the private row at CREATE.
			for _, q := range []string{
				"CREATE INDEX gin_am_private ON gin_am_contents USING gin(a,v)",
				"CREATE INDEX gin_am_private_partial ON gin_am_contents USING gin((COALESCE(a,'{}'::integer[]))) WHERE id<8",
			} {
				run(fixture.Exec(q, 0))
			}
			for _, name := range []string{"gin_am_private", "gin_am_private_partial"} {
				heapBoth(t, p, b, c, amcheckSelect("SELECT gin_index_check('"+name+"') AS ok", false))
			}
			run(fixture.Exec("UPDATE gin_am_contents SET a='{3}',v='cat:1' WHERE id=8", 1))
			for _, name := range []string{"gin_am_private", "gin_am_private_partial"} {
				heapBoth(t, p, b, c, amcheckSelect("SELECT gin_index_check('"+name+"') AS ok", false))
			}
			run(fixture.Exec("ROLLBACK", 0))
		}()
		contents(initial)
		check()
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('gin_am_private','gin_am_private_partial')", []string{"relname"}, []string{"NAME"}, nil))
		func() {
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("INSERT INTO gin_am_contents VALUES(8,'{3,3}',NULL)", 1))
			check()
			run(fixture.Exec("UPDATE gin_am_contents SET a='{}',v='' WHERE id=5", 1))
			check()
			run(fixture.Exec("DELETE FROM gin_am_contents WHERE id=4", 1))
			check()
			run(fixture.Exec("COMMIT", 0))
		}()
		contents([][]any{
			{int64(1), "{1,1,2}", "'cat':1,2 'dog':3"}, {int64(2), "{1,1,2}", "'cat':1,2 'dog':3"},
			{int64(3), "{NULL}", ""}, {int64(5), "{}", ""}, {int64(6), "[0:1]={2,3}", "'dog':1"},
			{int64(7), "{2,NULL,2}", "'cat':1 'dog':2"}, {int64(8), "{3,3}", nil},
		})
		check()
		heapBoth(t, p, b, c, ginIDs("SELECT id FROM gin_am_contents WHERE a @> '{3}'::integer[] ORDER BY id", []int64{6, 8}))
		heapBoth(t, p, b, c, ginIDs("SELECT id FROM gin_am_contents WHERE v @@ 'cat'::tsquery ORDER BY id", []int64{1, 2, 7}))
		func() {
			run(fixture.Exec("BEGIN READ ONLY", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			check()
			run(fixture.Exec("COMMIT", 0))
		}()
	})
}
