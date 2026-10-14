package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckIndexFailureDemand(t *testing.T) {
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
				for _, route := range []string{"btree_predicate", "btree_key", "hash_predicate", "hash_key"} {
					sequence := "domain_s.failure_" + route + "_steps"
					domainName := "failure_" + route + "_checked"
					domain := "domain_s." + domainName
					table := "domain_s.failure_" + route + "_target"
					run(fixture.Exec("CREATE SEQUENCE "+sequence, 0))
					run(fixture.Exec("CREATE DOMAIN "+domain+" AS integer CONSTRAINT a_effect CHECK(nextval('"+sequence+"')>0) CONSTRAINT z_positive CHECK(VALUE>0)", 0))
					run(fixture.Exec("CREATE TABLE "+table+"(id integer)", 0))
					definition := "CREATE INDEX failure_" + route + "_index ON " + table
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
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO "+table+" VALUES(1)", 1))
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Select("SELECT currval('"+sequence+"') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
					run(fixture.Exec("BEGIN", 0))
					run(domainCheckFailure("UPDATE "+table+" SET id=-1 WHERE id=1", domainName, "z_positive", false, prepared))
					run(fixture.Exec("ROLLBACK", 0))
					// The failed successor consumed its CHECKs, not the predecessor's;
					// sequence effects survive while row and index publication do not.
					run(fixture.Select("SELECT currval('"+sequence+"') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
					run(fixture.Select("SELECT id FROM "+table+" WHERE id=1", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("UPDATE "+table+" SET id=2 WHERE id=1", 1))
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Select("SELECT currval('"+sequence+"') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
					run(fixture.Select("SELECT id FROM "+table+" WHERE id=2", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
				}
			})
		})
	}
}
