package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgstatginindexFeatureSmoke(t *testing.T) {
	var emitted string
	pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE gg_part.gg_smoke(id integer,a integer[],v tsvector)", 0))
		run(fixture.Exec("INSERT INTO gg_part.gg_smoke VALUES(1,'{1,2,2}','cat:1 rat:2'),(2,'{}','dog:1'),(3,NULL,NULL)", 3))
		for _, q := range []string{"CREATE INDEX gg_smoke_a ON gg_part.gg_smoke USING gin(a) WITH(fastupdate=off)", "CREATE INDEX gg_smoke_v ON gg_part.gg_smoke USING gin(v) WITH(fastupdate=off)"} {
			run(fixture.Exec(q, 0))
		}
		pgstatginindexBoth(t, p, b, c, pgstatginindexStats("SELECT * FROM gg_part.pgstatginindex('gg_part.gg_smoke_a'::regclass)"))
		q := "SELECT * FROM gg_part.pgstatginindex($1::regclass)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		for _, arg := range []any{"gg_part.gg_smoke_a", "gg_part.gg_smoke_v", nil, "gg_part.gg_smoke_a"} {
			s := pgstatginindexStats(q, arg)
			if arg == nil {
				s = pgstatginindexNull(q, arg)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		array := "SELECT id FROM gg_part.gg_smoke WHERE a @> '{2}'::integer[] ORDER BY id"
		vector := "SELECT id FROM gg_part.gg_smoke WHERE v @@ 'cat <-> rat'::tsquery ORDER BY id"
		for _, q := range []string{array, vector} {
			pgstatginindexBoth(t, p, b, c, pgstatginindexIDs(q, 1))
		}
		run(fixture.Exec("UPDATE gg_part.gg_smoke SET a='{9}',v='dog:1' WHERE id=1", 1))
		for _, q := range []string{array, vector} {
			pgstatginindexBoth(t, p, b, c, pgstatginindexIDs(q))
		}
		pgstatginindexBoth(t, p, b, c, pgstatginindexNull("SELECT * FROM gg_part.pgstatginindex(NULL::regclass)"))
		pgstatginindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM gg_part.pgstatginindex('gg_part.gg_smoke'::regclass)", "42809", "relation \"gg_smoke\" is not a GIN index"))
		for _, q := range []string{"CREATE VIEW gg_part.gg_smoke_full AS SELECT * FROM gg_part.pgstatginindex('gg_part.gg_smoke_a'::regclass)", "CREATE VIEW gg_part.gg_smoke_field AS SELECT (gg_part.pgstatginindex('gg_part.gg_smoke_v'::regclass)).pending_tuples AS n"} {
			run(fixture.Exec(q, 0))
		}
		replayed := false
		stats := func(query string, args ...any) fixture.Step {
			if b.PG && replayed {
				// Replay loads three heap tuples before fastupdate=off is
				// restored; that setting does not clean an existing pending list.
				return fixture.Select("SELECT version,pending_pages>0 AS has_pending,pending_tuples FROM ("+query+") s", []string{"version", "has_pending", "pending_tuples"}, []string{"INT4", "BOOL", "INT8"}, [][]any{{int64(2), true, int64(3)}}, args...)
			}
			return pgstatginindexStats(query, args...)
		}
		check := func() {
			pending := int64(0)
			if b.PG && replayed {
				pending = 3
			}
			pgstatginindexBoth(t, p, b, c, stats("SELECT * FROM gg_part.gg_smoke_full"))
			pgstatginindexBoth(t, p, b, c, fixture.Select("SELECT n FROM gg_part.gg_smoke_field", []string{"n"}, []string{"INT8"}, [][]any{{pending}}))
		}
		check()
		if b.PG {
			if emitted != "" {
				for _, q := range []string{"DROP EXTENSION pgstattuple CASCADE", "DROP SCHEMA gg_part CASCADE"} {
					run(fixture.Exec(q, 0))
				}
				if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
					t.Fatal("smoke native stream replay", err)
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				replayed = true
			}
		} else {
			emitted = fixture.ExtensionDump(t, p.Ctx, p.Native)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgstatginindexStats(q, "gg_part.gg_smoke_v"))
		}
		check()
		run(fixture.Exec("INSERT INTO gg_part.gg_smoke VALUES(4,'{2}','cat:1 rat:2')", 1))
		for _, q := range []string{array, vector} {
			pgstatginindexBoth(t, p, b, c, pgstatginindexIDs(q, 4))
		}
		for _, name := range []string{"gg_smoke_a", "gg_smoke_v"} {
			pgstatginindexBoth(t, p, b, c, stats(q, "gg_part."+name))
		}
	})
}
