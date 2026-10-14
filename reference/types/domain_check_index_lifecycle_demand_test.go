package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckIndexLifecycleDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) {
					s.Prepared = prepared
					t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
					if t.Failed() {
						t.FailNow()
					}
				}
				for _, route := range []string{"btree_predicate", "btree_key", "unique_predicate", "hash_predicate", "hash_key"} {
					sequence := "domain_s.lifecycle_" + route + "_steps"
					domain := "domain_s.lifecycle_" + route + "_checked"
					tableName := "lifecycle_" + route + "_target"
					table := "domain_s." + tableName
					indexName := "lifecycle_" + route + "_index"
					run(fixture.Exec("CREATE SEQUENCE "+sequence, 0))
					run(fixture.Exec("CREATE DOMAIN "+domain+" AS integer CHECK(nextval('"+sequence+"')>0)", 0))
					run(fixture.Exec("CREATE TABLE "+table+"(id integer)", 0))
					definition := "CREATE INDEX "
					if route == "unique_predicate" {
						definition = "CREATE UNIQUE INDEX "
					}
					definition += indexName + " ON " + table
					if strings.HasPrefix(route, "hash_") {
						definition += " USING hash"
					}
					if strings.HasSuffix(route, "predicate") {
						definition += "(id) WHERE id::" + domain + ">0"
					} else {
						definition += "(((id::" + domain + ")::integer))"
					}
					if route == "hash_predicate" && !b.PG {
						s := fixture.ErrorDiagnostic(definition, fixture.ControlsReferenceDiagnostic("0A000", "partial indexes are unsupported"))
						s.ErrorStage = "execute"
						run(s)
						run(fixture.Select("SELECT nextval('"+sequence+"') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
						run(fixture.Select("SELECT id FROM "+table, []string{"id"}, []string{"INT4"}, nil))
						continue
					}
					run(fixture.Exec(definition, 0))
					run(fixture.Select("SELECT nextval('"+sequence+"') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					observe := func(n int64) {
						run(fixture.Select("SELECT currval('"+sequence+"') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{n}}))
					}
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO "+table+" VALUES(1)", 1))
					observe(2)
					run(fixture.Exec("COMMIT", 0))
					observe(2)
					n := int64(2)
					if route == "unique_predicate" {
						run(fixture.Exec("BEGIN", 0))
						d := fixture.ControlsReferenceDiagnostic("23505", "duplicate key value violates unique constraint \""+indexName+"\"")
						d.Detail, d.Schema, d.Table, d.Constraint = "Key (id)=(1) already exists.", "domain_s", tableName, indexName
						s := fixture.ErrorDiagnostic("INSERT INTO "+table+" VALUES(1)", d)
						s.ErrorStage = "execute"
						run(s)
						run(fixture.Exec("ROLLBACK", 0))
						n++
						observe(n)
						run(fixture.Select("SELECT id FROM "+table, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
					}
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("UPDATE "+table+" SET id=2 WHERE id=1", 1))
					n++
					observe(n)
					run(fixture.Exec("COMMIT", 0))
					observe(n)
					run(fixture.Select("SELECT id FROM "+table, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("DELETE FROM "+table+" WHERE id=2", 1))
					observe(n)
					run(fixture.Exec("COMMIT", 0))
					observe(n)
					run(fixture.Select("SELECT id FROM "+table, []string{"id"}, []string{"INT4"}, nil))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO "+table+" VALUES(1)", 1))
					n++
					observe(n)
					run(fixture.Exec("ROLLBACK", 0))
					observe(n)
					run(fixture.Select("SELECT id FROM "+table, []string{"id"}, []string{"INT4"}, nil))
				}
			})
		})
	}
}
