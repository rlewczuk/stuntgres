package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceDeparseRelationsIndexForms(t *testing.T) {
	schemaReferenceCleanup(t, "deparse_index")
	deparseRelationsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		deparseRelationsSeed(t, p, b, c)
		full := "CREATE INDEX ix ON public.t USING btree (id, txt DESC NULLS LAST, id NULLS FIRST)"
		for _, tc := range []struct {
			call string
			want any
		}{
			{"pg_get_indexdef('ix'::regclass)", full},
			{"pg_get_indexdef('ix'::regclass,0,false)", full},
			{"pg_catalog.pg_get_indexdef('ix'::regclass,0,true)", "CREATE INDEX ix ON t USING btree (id, txt DESC NULLS LAST, id NULLS FIRST)"},
			{"pg_get_indexdef('ix'::regclass,1,false)", "id"},
			{"pg_get_indexdef('ix'::regclass,2,true)", "txt"},
			{"pg_get_indexdef('ix'::regclass,3,false)", "id"},
			{"pg_get_indexdef('ix'::regclass,-1,false)", ""},
			{"pg_get_indexdef('ix'::regclass,4,false)", ""},
			{"pg_get_indexdef('ix'::regclass,(-2147483648)::int,false)", ""},
			{"pg_get_indexdef('ix'::regclass,2147483647,true)", ""},
			{"pg_get_indexdef(0::oid)", nil}, {"pg_get_indexdef(4294967295::oid)", nil},
			{"pg_get_indexdef('t'::regclass)", nil}, {"pg_get_indexdef('v'::regclass)", nil},
			{"pg_get_indexdef(NULL::oid)", nil}, {"pg_get_indexdef(NULL::oid,1,false)", nil},
			{"pg_get_indexdef('ix'::regclass,NULL::int,false)", nil}, {"pg_get_indexdef('ix'::regclass,0,NULL::boolean)", nil},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%v", tc.call, prepared), func(t *testing.T) {
					s := deparseRelationsTextStep(t, b, "SELECT "+tc.call+" AS definition", tc.want)
					if prepared {
						s = referencePrepared(s)
					}
					referenceRunStep(t, p.ctx, c, b.recorder, s)
				})
			}
		}
		for _, q := range []string{"CREATE TABLE u(c INT UNIQUE, n INT UNIQUE NULLS NOT DISTINCT)", `CREATE TABLE "select"("été" INT)`, `CREATE INDEX "Mixed Index" ON "select"("été" DESC)`, `ALTER TABLE "select" RENAME COLUMN "été" TO "from"`, `ALTER TABLE "select" RENAME TO "table"`} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(q, 0))
		}
		for _, tc := range []struct{ q, w string }{
			{"SELECT pg_get_indexdef('u_c_key'::regclass) AS definition", "CREATE UNIQUE INDEX u_c_key ON public.u USING btree (c)"},
			{"SELECT pg_get_indexdef('u_n_key'::regclass) AS definition", "CREATE UNIQUE INDEX u_n_key ON public.u USING btree (n) NULLS NOT DISTINCT"},
			{`SELECT pg_get_indexdef('"Mixed Index"'::regclass) AS definition`, `CREATE INDEX "Mixed Index" ON public."table" USING btree ("from" DESC)`},
		} {
			t.Run(tc.q, func(t *testing.T) {
				referenceRunStep(t, p.ctx, c, b.recorder, deparseRelationsTextStep(t, b, tc.q, tc.w))
			})
		}
		empty := deparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('ix'::regclass) AS definition FROM t WHERE false", nil)
		empty.rows = nil
		referenceRunStep(t, p.ctx, c, b.recorder, empty)
		for _, q := range []string{"CREATE SCHEMA deparse_index", "ALTER TABLE t RENAME COLUMN txt TO renamed_txt", "ALTER TABLE t SET SCHEMA deparse_index", "CREATE TABLE t(id INT,txt TEXT)"} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(q, 0))
		}
		referenceRunStep(t, p.ctx, c, b.recorder, deparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('deparse_index.ix'::regclass,2,false) AS definition", "renamed_txt"))
		referenceRunStep(t, p.ctx, c, b.recorder, deparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('deparse_index.ix'::regclass) AS definition", "CREATE INDEX ix ON deparse_index.t USING btree (id, renamed_txt DESC NULLS LAST, id NULLS FIRST)"))
	})
}
