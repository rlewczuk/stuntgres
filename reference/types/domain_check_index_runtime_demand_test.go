package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckIndexRuntimeDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) {
					s.Prepared = prepared
					t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
					if t.Failed() {
						t.FailNow()
					}
				}
				for _, route := range []string{"predicate", "key"} {
					sequence := "domain_s.index_" + route + "_steps"
					domain := "domain_s.index_" + route + "_checked"
					table := "domain_s.index_" + route + "_target"
					run(fixture.Exec("CREATE SEQUENCE "+sequence, 0))
					run(fixture.Exec("CREATE DOMAIN "+domain+" AS integer CHECK(nextval('"+sequence+"')>0)", 0))
					run(fixture.Exec("CREATE TABLE "+table+"(id integer)", 0))
					definition := "CREATE INDEX index_" + route + "_runtime ON " + table
					if route == "predicate" {
						definition += "(id) WHERE id::" + domain + ">0"
					} else {
						definition += "(((id::" + domain + ")::integer))"
					}
					run(fixture.Exec(definition, 0))
					// An empty build initializes recipes but has no candidate truth.
					run(fixture.Select("SELECT nextval('"+sequence+"') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO "+table+" VALUES(1)", 1))
					// INSERT owns one produced predicate membership/key. COMMIT
					// publishes that result, not another conversion of the same row.
					run(fixture.Select("SELECT currval('"+sequence+"') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Select("SELECT currval('"+sequence+"') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
					run(fixture.Select("SELECT id FROM "+table, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				}
			})
		})
	}
}
