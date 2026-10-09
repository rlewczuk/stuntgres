package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
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
	t.Run("clauses-presentation", func(t *testing.T) {
		deparseRelationsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(n INT,s TEXT,p TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(-1,'a ','space'),(1,'a','plain')", 2))
			run(referenceExec(`CREATE INDEX cover ON t(n int4_ops,lower(s) COLLATE "POSIX" bpchar_ops DESC NULLS LAST,s COLLATE "default" text_ops) INCLUDE(p,s)`, 0))
			run(referenceSelect("SELECT relnatts FROM pg_class WHERE relname='cover'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(5)}}))
			check := func(renamed bool) {
				full := `CREATE INDEX cover ON public.t USING btree (n, lower(s) COLLATE "POSIX" bpchar_ops DESC NULLS LAST, s) INCLUDE (p, s)`
				pretty := `CREATE INDEX cover ON t USING btree (n, lower(s) COLLATE "POSIX" bpchar_ops DESC NULLS LAST, s) INCLUDE (p, s)`
				columns := []string{"n", "lower(s)", "s", "p", "s", ""}
				if renamed {
					full = `CREATE INDEX cover ON public.t USING btree (n, lower("from") COLLATE "POSIX" bpchar_ops DESC NULLS LAST, "from") INCLUDE (payload, "from")`
					pretty = `CREATE INDEX cover ON t USING btree (n, lower("from") COLLATE "POSIX" bpchar_ops DESC NULLS LAST, "from") INCLUDE (payload, "from")`
					columns = []string{"n", `lower("from")`, `"from"`, "payload", `"from"`, ""}
				}
				var before bytes.Buffer
				if !b.pg {
					if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
						t.Fatal(err)
					}
				}
				for _, tc := range []struct{ call, want string }{
					{"pg_get_indexdef('cover'::regclass)", full},
					{"pg_get_indexdef('cover'::regclass,0,false)", full},
					{"pg_get_indexdef('cover'::regclass,0,true)", pretty},
				} {
					s := deparseRelationsTextStep(t, b, "SELECT "+tc.call+" AS definition", tc.want)
					run(s)
					run(referencePrepared(s))
				}
				for i, want := range columns {
					for _, pretty := range []bool{false, true} {
						q := fmt.Sprintf("SELECT pg_get_indexdef('cover'::regclass,%d,%v) AS definition", i+1, pretty)
						s := deparseRelationsTextStep(t, b, q, want)
						run(s)
						run(referencePrepared(s))
					}
				}
				if !b.pg {
					var after bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || after.String() != before.String() {
						t.Fatal("inquiry presentation mutated canonical descriptor/Dump", err)
					}
					want := `CREATE INDEX "cover" ON "public"."t" USING btree ("n" "pg_catalog"."int4_ops" ASC NULLS LAST, (pg_catalog.lower("s")) COLLATE "pg_catalog"."POSIX" "pg_catalog"."bpchar_ops" DESC NULLS LAST, "s" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST) INCLUDE ("p", "s");`
					if renamed {
						want = `CREATE INDEX "cover" ON "public"."t" USING btree ("n" "pg_catalog"."int4_ops" ASC NULLS LAST, (pg_catalog.lower("from")) COLLATE "pg_catalog"."POSIX" "pg_catalog"."bpchar_ops" DESC NULLS LAST, "from" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST) INCLUDE ("payload", "from");`
					}
					if !strings.Contains(after.String(), want) {
						t.Fatalf("inquiry spelling leaked into canonical Dump: %s", after.String())
					}
				}
			}
			check(false)
			run(referenceExec(`ALTER TABLE t RENAME s TO "from"`, 0))
			run(referenceExec("ALTER TABLE t RENAME p TO payload", 0))
			check(true)
			run(referencePrepared(referenceSelect(`SELECT n,lower("from") AS lowered,payload FROM t ORDER BY n`, []string{"n", "lowered", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(-1), "a ", "space"}, {int64(1), "a", "plain"}})))
		})
	})
	t.Run("class-identity-alias", func(t *testing.T) {
		deparseRelationsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(s VARCHAR,p TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES('a ','payload')", 1))
			run(referenceExec("CREATE INDEX alias_cover ON t(s pg_catalog.varchar_ops) INCLUDE(p)", 0))
			run(referenceExec("CREATE INDEX default_cover ON t(s pg_catalog.text_ops) INCLUDE(p)", 0))
			for _, tc := range []struct{ name, provider string }{{"alias_cover", " varchar_ops"}, {"default_cover", ""}} {
				q := "SELECT pg_get_indexdef('" + tc.name + "'::regclass) AS definition"
				s := deparseRelationsTextStep(t, b, q, "CREATE INDEX "+tc.name+" ON public.t USING btree (s"+tc.provider+") INCLUDE (p)")
				run(s)
				run(referencePrepared(s))
			}
			run(referencePrepared(referenceSelect("SELECT s,p FROM t", []string{"s", "p"}, []string{"VARCHAR", "TEXT"}, [][]any{{"a ", "payload"}})))
			if !b.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				want := `CREATE INDEX "alias_cover" ON "public"."t" USING btree ("s" "pg_catalog"."varchar_ops" ASC NULLS LAST) INCLUDE ("p");`
				if !strings.Contains(dump.String(), want) {
					t.Fatalf("family equivalence erased actual class identity: %s", dump.String())
				}
			}
		})
	})
	t.Run("expression-shapes", func(t *testing.T) {
		deparseRelationsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(n INT,p TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(-1,'minus'),(1,'plus')", 2))
			for _, tc := range []struct{ name, input, expression, prettyExpression string }{
				{"coalesce", "coalesce(n,n)", "COALESCE(n, n)", "COALESCE(n, n)"},
				{"nullif", "nullif(n,0)", "NULLIF(n, 0)", "NULLIF(n, 0)"},
				{"greatest", "greatest(n,0)", "GREATEST(n, 0)", "GREATEST(n, 0)"},
				{"least", "least(n,0)", "LEAST(n, 0)", "LEAST(n, 0)"},
				{"ordinary", "abs(n)", "abs(n)", "abs(n)"},
				{"operator", "n+0", "((n + 0))", "(n + 0)"},
			} {
				for _, bare := range []bool{false, true} {
					if bare && tc.name == "operator" {
						continue
					}
					name := "shape_" + tc.name
					element := "(" + tc.input + ")"
					if bare {
						name += "_bare"
						element = tc.input
					}
					run(referenceExec("CREATE INDEX "+name+" ON t("+element+" oid_ops) INCLUDE(p)", 0))
					for _, pretty := range []bool{false, true} {
						expression, parent := tc.expression, "public.t"
						if pretty {
							expression, parent = tc.prettyExpression, "t"
						}
						full := "CREATE INDEX " + name + " ON " + parent + " USING btree (" + expression + " oid_ops) INCLUDE (p)"
						for _, item := range []struct {
							ordinal int
							want    string
						}{{0, full}, {1, expression}, {2, "p"}} {
							q := fmt.Sprintf("SELECT pg_get_indexdef('%s'::regclass,%d,%v) AS definition", name, item.ordinal, pretty)
							s := deparseRelationsTextStep(t, b, q, item.want)
							run(s)
							run(referencePrepared(s))
						}
					}
				}
			}
			run(referenceSelect("SELECT n,p FROM t ORDER BY n", []string{"n", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(-1), "minus"}, {int64(1), "plus"}}))
		})
	})
}
