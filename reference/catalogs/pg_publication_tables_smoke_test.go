package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGPublicationTablesFeatureSmoke(t *testing.T) {
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := publicationConn(t, p, b)
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run := func(steps ...fixture.Step) { publicationRun(t, p, b, c, steps...) }
		run(fixture.Exec("CREATE SCHEMA publication_rt_s", 0),
			fixture.Exec("CREATE TABLE publication_rt_s.t(id integer,v integer)", 0),
			fixture.Exec("INSERT INTO publication_rt_s.t VALUES(1,-1),(2,2)", 2),
			fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_s.t(v,id) WHERE(v>0) WITH(publish='insert')", 0))
		filtered := []any{"publication_rt_p", "publication_rt_s", "t", "{id,v}", "(v > 0)"}
		for _, prepared := range []bool{false, true} {
			publicationTablesRead(t, p, b, c, publicationTablesSQL, prepared, filtered)
		}
		stmt, err := c.PrepareContext(p.Ctx, publicationTablesSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: publicationTablesSQL}
		state := func(rows ...[]any) { publicationRun(t, p, b, held, publicationTablesState(rows...)) }
		state(filtered)
		// Preserve the initial reversed-list witness, then remove the list:
		// PostgreSQL forbids column lists alongside same-publication schemas.
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_s.t WHERE(v>0)", 0))
		state(filtered)
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLES IN SCHEMA publication_rt_s", 0))
		broad := []any{"publication_rt_p", "publication_rt_s", "t", "{id,v}", nil}
		future := []any{"publication_rt_p", "publication_rt_s", "u", "{id}", nil}
		state(broad)
		run(fixture.Exec("CREATE TABLE publication_rt_s.u(id integer)", 0))
		state(broad, future)
		run(fixture.Exec("BEGIN", 0), fixture.Exec("ALTER PUBLICATION publication_rt_p DROP TABLES IN SCHEMA publication_rt_s", 0))
		state(filtered)
		run(fixture.Exec("ROLLBACK", 0))
		state(broad, future)
		run(fixture.Exec("CREATE TEMP TABLE publication_rt_temp(id integer)", 0))
		for _, prepared := range []bool{false, true} {
			s := publicationTablesTemporaryDiagnostic()
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			state(broad, future)
		}
		// The publication filter never filters ordinary local SQL or starts
		// transport. No slot, subscription, WAL reader or network is involved.
		run(publicationTablesLocalRows())
		publicationTablesRoundtrip(t, p, b, broad, future)
		state(broad, future)
	})
}
