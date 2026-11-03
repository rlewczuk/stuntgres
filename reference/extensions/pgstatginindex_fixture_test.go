package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var pgstatginindexNames = []string{"version", "pending_pages", "pending_tuples"}
var pgstatginindexTypes = []string{"INT4", "INT4", "INT8"}

func pgstatginindexCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "gg_part")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS pgstattuple CASCADE", "DROP SCHEMA IF EXISTS gg_part CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			for _, q := range []string{"CREATE SCHEMA gg_part", "CREATE EXTENSION pgstattuple SCHEMA gg_part", "CREATE TABLE gg_part.gg_heap(id integer PRIMARY KEY,a integer[],v tsvector)"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func pgstatginindexBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func pgstatginindexStats(query string, args ...any) fixture.Step {
	return fixture.Select(query, pgstatginindexNames, pgstatginindexTypes, [][]any{{int64(2), int64(0), int64(0)}}, args...)
}

func pgstatginindexNull(query string, args ...any) fixture.Step {
	return fixture.Select(query, pgstatginindexNames, pgstatginindexTypes, [][]any{{nil, nil, nil}}, args...)
}

func pgstatginindexBuild(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{"CREATE INDEX gg_array ON gg_part.gg_heap USING gin(a) WITH(fastupdate=off)", "CREATE INDEX gg_vector ON gg_part.gg_heap USING gin(v) WITH(fastupdate=off)"} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
}

func pgstatginindexSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO gg_part.gg_heap VALUES(1,'{1,2,2}','cat:1 rat:2'),(2,'{2,3}','dog:1'),(3,'{}',''),(4,NULL,NULL)", 4))
	pgstatginindexBuild(t, p, b, c)
}

func pgstatginindexIDs(query string, ids ...int64) fixture.Step {
	rows := make([][]any, len(ids))
	for i, id := range ids {
		rows[i] = []any{id}
	}
	return fixture.Select(query, []string{"id"}, []string{"INT4"}, rows)
}

const ggArrayQuery = "SELECT id FROM gg_part.gg_heap WHERE a @> '{2}'::integer[] ORDER BY id"
const ggVectorQuery = "SELECT id FROM gg_part.gg_heap WHERE v @@ 'cat <-> rat'::tsquery ORDER BY id"

func pgstatginindexContents(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, array, vector []int64) {
	t.Helper()
	pgstatginindexBoth(t, p, b, c, pgstatginindexIDs(ggArrayQuery, array...))
	pgstatginindexBoth(t, p, b, c, pgstatginindexIDs(ggVectorQuery, vector...))
	for _, name := range []string{"gg_array", "gg_vector"} {
		pgstatginindexBoth(t, p, b, c, pgstatginindexStats("SELECT * FROM gg_part.pgstatginindex($1::regclass)", "gg_part."+name))
	}
}
