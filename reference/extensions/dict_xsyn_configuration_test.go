package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDictXsynConfiguration(t *testing.T) {
	xsynCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE EXTENSION dict_xsyn",
			"ALTER TEXT SEARCH DICTIONARY xsyn (rules=xsyn_sample)",
			"CREATE TEXT SEARCH CONFIGURATION xsyn_cfg (COPY=pg_catalog.simple)",
			"ALTER TEXT SEARCH CONFIGURATION xsyn_cfg ALTER MAPPING FOR asciiword WITH xsyn,pg_catalog.simple",
			"CREATE TABLE xsyn_vectors(id integer,v tsvector)",
		} {
			xsynExec(t, p, b, c, q, 0)
		}
		const q = "SELECT to_tsvector('xsyn_cfg',$1::text)::text AS vector"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		check := func(stage, want string) {
			t.Helper()
			if !t.Run(stage, func(t *testing.T) {
				// Explicitly authorized PostgreSQL18.6 oracle exception:
				// dxsyn_lexize leaves flags uninitialized for recognized-empty
				// results, and vector conversion can crash the server. Native
				// vector assertions and dual-mode ts_lexize matrix remain exact.
				if b.PG && (stage == "consume_original" || stage == "consume_synonyms" || stage == "private_empty") {
					t.Skip("authorized PostgreSQL18.6 dxsyn_lexize uninitialized-flags defect: https://raw.githubusercontent.com/postgres/postgres/REL_18_6/contrib/dict_xsyn/dict_xsyn.c (LexizeExec consumer: src/backend/tsearch/ts_parse.c)")
				}
				for _, route := range []struct {
					name     string
					executor fixture.Executor
				}{{"direct", c}, {"held", h}} {
					if !t.Run(route.name, func(t *testing.T) {
						xsynRun(t, p, b, route.executor, fixture.Select(q, []string{"vector"}, []string{"TEXT"}, [][]any{{want}}, "supernova sn grb"))
					}) {
						t.FailNow()
					}
				}
			}) {
				t.FailNow()
			}
		}
		check("sample", "'1987a':1 'grb':3 'sn':1,2 'sne':1 'supernova':1")
		xsynExec(t, p, b, c, "INSERT INTO xsyn_vectors VALUES(1,to_tsvector('xsyn_cfg','supernova sn grb'))", 1)
		xsynSelect(t, p, b, c, "SELECT to_tsquery('xsyn_cfg','supernova')::text AS query", []string{"query"}, []string{"TEXT"}, [][]any{{"'supernova' & 'sn' & 'sne' & '1987a'"}})
		xsynExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY xsyn (keeporig=false,keepsynonyms=false)", 0)
		check("consume_original", "'grb':3 'sn':2")
		if b.PG {
			// Avoid only the unsafe conversion; stored vectors remain verified.
			xsynExec(t, p, b, c, "INSERT INTO xsyn_vectors VALUES(2,'''grb'':3 ''sn'':2'::tsvector)", 1)
		} else {
			xsynExec(t, p, b, c, "INSERT INTO xsyn_vectors VALUES(2,to_tsvector('xsyn_cfg','supernova sn grb'))", 1)
		}
		xsynExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY xsyn (matchsynonyms=true)", 0)
		check("consume_synonyms", "'grb':3")
		if b.PG {
			xsynExec(t, p, b, c, "INSERT INTO xsyn_vectors VALUES(3,'''grb'':3'::tsvector)", 1)
		} else {
			xsynExec(t, p, b, c, "INSERT INTO xsyn_vectors VALUES(3,to_tsvector('xsyn_cfg','supernova sn grb'))", 1)
		}
		xsynExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY xsyn (keeporig,keepsynonyms)", 0)
		xsynSelect(t, p, b, c, "SELECT to_tsvector('xsyn_cfg','supernova') @@ to_tsquery('xsyn_cfg','sn') AS matched", []string{"matched"}, []string{"BOOL"}, [][]any{{true}})
		xsynSelect(t, p, b, c, "SELECT id,v::text FROM xsyn_vectors ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "'1987a':1 'grb':3 'sn':1,2 'sne':1 'supernova':1"}, {int64(2), "'grb':3 'sn':2"}, {int64(3), "'grb':3"}})
		// Private ALTER and its rollback restore executable group state, not
		// merely an extension row. A successful savepoint rollback does too.
		xsynExec(t, p, b, c, "BEGIN", 0)
		xsynExec(t, p, b, c, "SAVEPOINT xsyn_settings", 0)
		xsynExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY xsyn (rules)", 0)
		check("absent_source_in_savepoint", "'grb':3 'sn':2 'supernova':1")
		xsynExec(t, p, b, c, "ROLLBACK TO SAVEPOINT xsyn_settings", 0)
		check("restored_savepoint", "'1987a':1,2 'grb':3 'sn':1,2 'sne':1,2 'supernova':1,2")
		xsynExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY xsyn (keeporig=false,keepsynonyms=false)", 0)
		check("private_empty", "'grb':3")
		xsynExec(t, p, b, c, "ROLLBACK", 0)
		check("restored_transaction", "'1987a':1,2 'grb':3 'sn':1,2 'sne':1,2 'supernova':1,2")
	})
}
