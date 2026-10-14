package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckIndexConflictDemand(t *testing.T) {
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
				for _, route := range []string{"predicate", "key", "zero_width"} {
					sequence := "domain_s.conflict_" + route + "_steps"
					domain := "domain_s.conflict_" + route + "_checked"
					table := "domain_s.conflict_" + route + "_target"
					run(fixture.Exec("CREATE SEQUENCE "+sequence, 0))
					run(fixture.Exec("CREATE DOMAIN "+domain+" AS integer CHECK(nextval('"+sequence+"')>0)", 0))
					columns := "(id integer)"
					if route == "zero_width" {
						columns = "()"
					}
					run(fixture.Exec("CREATE TABLE "+table+columns, 0))
					definition := "CREATE UNIQUE INDEX conflict_" + route + "_index ON " + table
					inference := "(id) WHERE id::" + domain + ">0"
					if route == "predicate" {
						definition += "(id) WHERE id::" + domain + ">0"
					} else {
						value := "id"
						if route == "zero_width" {
							value = "1"
						}
						expression := "((" + value + "::" + domain + ")::integer)"
						definition += "(" + expression + ")"
						inference = "(" + expression + ")"
					}
					if route != "predicate" && !b.PG {
						d := fixture.ControlsReferenceDiagnostic("0A000", "unique expression indexes are unsupported")
						d.Position = strconv.Itoa(strings.Index(definition, "((") + 2)
						s := fixture.ErrorDiagnostic(definition, d)
						s.ErrorStage = "execute"
						run(s)
						run(fixture.Select("SELECT nextval('"+sequence+"') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
						run(fixture.Select("SELECT count(*) AS n FROM "+table, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
						continue
					}
					run(fixture.Exec(definition, 0))
					run(fixture.Select("SELECT nextval('"+sequence+"') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					observe := func(n int64) {
						run(fixture.Select("SELECT currval('"+sequence+"') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{n}}))
					}
					ordinary := "INSERT INTO " + table + " VALUES(1)"
					accepted := "INSERT INTO " + table + " VALUES(2) ON CONFLICT" + inference + " DO NOTHING"
					if route == "zero_width" {
						ordinary = "INSERT INTO " + table + " DEFAULT VALUES"
						accepted = ordinary + " ON CONFLICT" + inference + " DO NOTHING"
					}
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec(ordinary, 1))
					observe(2)
					run(fixture.Exec("COMMIT", 0))
					observe(2)
					if route == "zero_width" {
						run(fixture.Exec("DELETE FROM "+table, 1))
						observe(2)
					}
					// Conflict precheck and actual index insertion are distinct
					// owners, unlike internal probes of one ordinary insertion.
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec(accepted, 1))
					observe(4)
					run(fixture.Exec("COMMIT", 0))
					observe(4)
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec(accepted, 0))
					duplicateN := int64(5)
					if route != "predicate" {
						// Expression-key conflict checking also materializes the
						// conflicting heap tuple's key, not its predicate.
						duplicateN = 6
					}
					observe(duplicateN)
					run(fixture.Exec("COMMIT", 0))
					observe(duplicateN)
					if route == "zero_width" {
						run(fixture.Select("SELECT count(*) AS n FROM "+table, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					} else {
						run(fixture.Select("SELECT id FROM "+table+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					}
				}
			})
		})
	}
}
