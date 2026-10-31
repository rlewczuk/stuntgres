package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstatindexContents(t *testing.T) {
	pgstatindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE ps_part.ps_members(k integer, payload text)", 0))
		run(fixture.Exec("INSERT INTO ps_part.ps_members VALUES(-1,'outside'),(-2,'also outside')", 2))
		run(fixture.Exec("CREATE INDEX ps_partial ON ps_part.ps_members(k) WHERE k>0", 0))
		run(fixture.Exec("CREATE INDEX ps_expression ON ps_part.ps_members((k+1)) INCLUDE(payload) WHERE k>0", 0))
		// Real heap rows exist, but neither target index contains any of them.
		for _, name := range []string{"ps_partial", "ps_expression"} {
			for _, typ := range []string{"text", "regclass"} {
				pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part.pgstatindex('ps_part."+name+"'::"+typ+")")
			}
		}
		run(fixture.Exec("INSERT INTO ps_part.ps_members SELECT g,'included payload' FROM generate_series(1,600) g", 600))
		for _, name := range []string{"ps_partial", "ps_expression"} {
			for _, typ := range []string{"text", "regclass"} {
				pgstatindexPopulated(t, p, c, b.Recorder, "ps_part.pgstatindex('ps_part."+name+"'::"+typ+")")
			}
		}
		pgstatindexBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM ps_part.ps_members WHERE k>0", []string{"n"}, []string{"INT8"}, [][]any{{int64(600)}}))
		run(fixture.Exec("UPDATE ps_part.ps_members SET k=k+1000 WHERE k>0", 600))
		run(fixture.Exec("DELETE FROM ps_part.ps_members WHERE k>1300", 300))
		for _, name := range []string{"ps_partial", "ps_expression"} {
			pgstatindexPopulated(t, p, c, b.Recorder, "ps_part.pgstatindex('ps_part."+name+"'::regclass)")
		}
		pgstatindexBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM ps_part.ps_members WHERE k>0", []string{"n"}, []string{"INT8"}, [][]any{{int64(300)}}))
		// TRUNCATE rebuilds the empty index; DELETE alone need not release PG pages.
		run(fixture.Exec("TRUNCATE ps_part.ps_members", 0))
		for _, name := range []string{"ps_partial", "ps_expression"} {
			pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part.pgstatindex('ps_part."+name+"'::text)")
		}
	})
}
