package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstatginindexValues(t *testing.T) {
	pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		pgstatginindexBuild(t, p, b, c)
		pgstatginindexContents(t, p, b, c, nil, nil)
		run(fixture.Exec("INSERT INTO gg_part.gg_heap VALUES(1,'{2,2}','cat:1 rat:2')", 1))
		pgstatginindexContents(t, p, b, c, []int64{1}, []int64{1})
		for _, q := range []string{"CREATE TABLE gg_part.gg_fast(id integer,a integer[])", "CREATE INDEX gg_fast_index ON gg_part.gg_fast USING gin(a)"} {
			run(fixture.Exec(q, 0))
		}
		pgstatginindexBoth(t, p, b, c, pgstatginindexStats("SELECT * FROM gg_part.pgstatginindex('gg_part.gg_fast_index')"))
		run(fixture.Exec("INSERT INTO gg_part.gg_fast VALUES(1,'{1,2,2}')", 1))
		q := "SELECT * FROM gg_part.pgstatginindex('gg_part.gg_fast_index')"
		check := func(rows *sql.Rows, err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			names, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(names, pgstatginindexNames) {
				t.Fatal(names, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 3 {
				t.Fatal(types, err)
			}
			for i, typ := range types {
				if typ.DatabaseTypeName() != pgstatginindexTypes[i] {
					t.Fatal(typ)
				}
			}
			if !rows.Next() {
				t.Fatal("missing pending row", rows.Err())
			}
			var version, pages, tuples int64
			if err := rows.Scan(&version, &pages, &tuples); err != nil {
				t.Fatal(err)
			}
			// Released fastupdate counts one inserted heap tuple, not three
			// array elements or two unique keys. Native's physical model remains
			// (2,0,0), independently of its logical pending token postings.
			if version != 2 || b.PG && (pages <= 0 || tuples != 1) || !b.PG && (pages != 0 || tuples != 0) {
				t.Fatalf("pending (%d,%d,%d), postgres=%v", version, pages, tuples, b.PG)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("pending tail", rows.Err())
			}
		}
		check(c.QueryContext(p.Ctx, q))
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		check(st.QueryContext(p.Ctx))
		pgstatginindexBoth(t, p, b, c, pgstatginindexIDs("SELECT id FROM gg_part.gg_fast WHERE a @> '{2}'::integer[] ORDER BY id", 1))
		for _, q := range []string{"ALTER INDEX gg_part.gg_fast_index SET(fastupdate=off)", "ALTER INDEX gg_part.gg_fast_index SET(gin_pending_list_limit=128)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("DELETE FROM gg_part.gg_fast", 1))
		pgstatginindexBoth(t, p, b, c, pgstatginindexIDs("SELECT id FROM gg_part.gg_fast WHERE a @> '{2}'::integer[] ORDER BY id"))
		// Turning fastupdate off or deleting rows does not promise to clean a
		// PostgreSQL pending list; native's physical model remains (2,0,0),
		// independently of logical pending postings and cleanup.
		if !b.PG {
			pgstatginindexBoth(t, p, b, c, pgstatginindexStats(q))
		}
	})
}

func TestReferencePgstatginindexContents(t *testing.T) {
	pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		pgstatginindexSeed(t, p, b, c)
		pgstatginindexContents(t, p, b, c, []int64{1, 2}, []int64{1})
		pgstatginindexBoth(t, p, b, c, fixture.Select("SELECT 'gg_part.gg_array'::regclass::oid <> 'gg_part.gg_vector'::regclass::oid AS distinct_indexes", []string{"distinct_indexes"}, []string{"BOOL"}, [][]any{{true}}))
		for _, q := range []string{"CREATE INDEX gg_partial ON gg_part.gg_heap USING gin(a) WITH(fastupdate=off) WHERE id>1", "CREATE INDEX gg_expression ON gg_part.gg_heap USING gin(coalesce(a,'{}'::integer[])) WITH(fastupdate=off)"} {
			run(fixture.Exec(q, 0))
		}
		for _, name := range []string{"gg_partial", "gg_expression"} {
			pgstatginindexBoth(t, p, b, c, pgstatginindexStats("SELECT * FROM gg_part.pgstatginindex($1::regclass)", "gg_part."+name))
		}
		pgstatginindexBoth(t, p, b, c, pgstatginindexIDs("SELECT id FROM gg_part.gg_heap WHERE id>1 AND a @> '{2}'::integer[] ORDER BY id", 2))
		pgstatginindexBoth(t, p, b, c, pgstatginindexIDs("SELECT id FROM gg_part.gg_heap WHERE coalesce(a,'{}'::integer[]) @> '{2}'::integer[] ORDER BY id", 1, 2))
		run(fixture.Exec("INSERT INTO gg_part.gg_heap VALUES(5,'{2}','cat:1 rat:2')", 1))
		pgstatginindexContents(t, p, b, c, []int64{1, 2, 5}, []int64{1, 5})
		run(fixture.Exec("UPDATE gg_part.gg_heap SET a='{9}',v='dog:1' WHERE id=1", 1))
		pgstatginindexContents(t, p, b, c, []int64{2, 5}, []int64{5})
		run(fixture.Exec("DELETE FROM gg_part.gg_heap WHERE id=2", 1))
		pgstatginindexContents(t, p, b, c, []int64{5}, []int64{5})
		pgstatginindexBoth(t, p, b, c, pgstatginindexIDs("SELECT id FROM gg_part.gg_heap ORDER BY id", 1, 3, 4, 5))
		pgstatginindexBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM gg_part.gg_heap", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
	})
}

func TestReferencePgstatginindexTransactions(t *testing.T) {
	pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		pgstatginindexSeed(t, p, b, c)
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO gg_part.gg_heap VALUES(5,'{2}','cat:1 rat:2'),(6,'{2}','dog:1')", 2))
		pgstatginindexContents(t, p, b, c, []int64{1, 2, 5, 6}, []int64{1, 5})
		pgstatginindexContents(t, p, b, peer, []int64{1, 2}, []int64{1})
		run(fixture.Exec("SAVEPOINT gg_save", 0))
		run(fixture.Exec("UPDATE gg_part.gg_heap SET a='{9}',v='dog:1' WHERE id=1", 1))
		run(fixture.Exec("DELETE FROM gg_part.gg_heap WHERE id=2", 1))
		pgstatginindexContents(t, p, b, c, []int64{5, 6}, []int64{5})
		run(fixture.Exec("ROLLBACK TO SAVEPOINT gg_save", 0))
		pgstatginindexContents(t, p, b, c, []int64{1, 2, 5, 6}, []int64{1, 5})
		run(fixture.Exec("ROLLBACK", 0))
		pgstatginindexContents(t, p, b, c, []int64{1, 2}, []int64{1})
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO gg_part.gg_heap VALUES(5,'{2}','cat:1 rat:2')", 1))
		pgstatginindexContents(t, p, b, peer, []int64{1, 2}, []int64{1})
		run(fixture.Exec("COMMIT", 0))
		pgstatginindexContents(t, p, b, peer, []int64{1, 2, 5}, []int64{1, 5})
		run(fixture.Exec("BEGIN READ ONLY", 0))
		pgstatginindexContents(t, p, b, c, []int64{1, 2, 5}, []int64{1, 5})
		run(fixture.Exec("COMMIT", 0))
		// The first tuple must not leak if a later tuple violates the real PK.
		s := fixture.ImplicitError("INSERT INTO gg_part.gg_heap VALUES(7,'{2}','cat:1 rat:2'),(5,'{9}','dog:1')", fixture.Diagnostic{Code: "23505", Message: "duplicate key value violates unique constraint \"gg_heap_pkey\"", Detail: "Key (id)=(5) already exists.", Schema: "gg_part", Table: "gg_heap", Constraint: "gg_heap_pkey"})
		s.ErrorStage = "execute"
		run(s)
		pgstatginindexContents(t, p, b, c, []int64{1, 2, 5}, []int64{1, 5})
		pgstatginindexBoth(t, p, b, c, pgstatginindexIDs("SELECT id FROM gg_part.gg_heap ORDER BY id", 1, 2, 3, 4, 5))
	})
}
