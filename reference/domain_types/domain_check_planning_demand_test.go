package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckPlanningDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) {
					if len(s.Query) >= 5 && s.Query[:5] == "MERGE" {
						s.Prepared = prepared || s.Prepared
						// Same public adapter contract as fixture.MergeRun:
						// lib/pq reports 0; native reports the actual effects.
						// Rows and sequence state independently prove effects.
						if b.PG {
							s.Affected = 0
						}
					}
					if !t.Run(s.Query, func(t *testing.T) {
						fixture.RunStep(t, p.Ctx, c, r, s)
					}) {
						t.FailNow()
					}
				}
				run(fixture.Exec("CREATE DOMAIN domain_s.plan_positive AS integer CONSTRAINT plan_positive_check CHECK(VALUE>0)", 0))
				for _, name := range []string{"plan_l", "plan_r", "merge_l", "merge_r"} {
					run(fixture.Exec("CREATE TABLE domain_s."+name+"(id integer)", 0))
				}
				full := func(condition string) string {
					return "SELECT l.id AS lid,r.id AS rid FROM domain_s.plan_l l FULL JOIN domain_s.plan_r r ON l.id=r.id AND (" + condition + ")"
				}
				merge := func(condition string) string {
					return "MERGE INTO domain_s.merge_l l USING domain_s.merge_r r ON l.id=r.id AND (" + condition + ") WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED BY TARGET THEN INSERT(id) VALUES(r.id)"
				}
				for _, condition := range []string{
					"0::domain_s.plan_positive>0",
					"(ARRAY[0]::domain_s.plan_positive[])[1]>0",
					"CASE 0::domain_s.plan_positive WHEN 1 THEN true ELSE false END",
					"CASE WHEN 0::domain_s.plan_positive>0 THEN true ELSE false END",
					"COALESCE(0::domain_s.plan_positive>0,true)",
				} {
					t.Run(condition, func(t *testing.T) {
						s := fixture.Select(full(condition), []string{"lid", "rid"}, []string{"INT4", "INT4"}, nil)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, r, s)
						s = fixture.Exec(merge(condition), 0)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, r, s)
					})
				}
				// Real SQL sequence state distinguishes planning from consuming a
				// join pair. Empty inputs consume none; one pair consumes one.
				run(fixture.Exec("CREATE SEQUENCE domain_s.plan_steps START 1", 0))
				run(fixture.Exec("CREATE DOMAIN domain_s.plan_each AS integer CONSTRAINT plan_each_check CHECK(nextval('domain_s.plan_steps'::regclass)>0 AND VALUE>0)", 0))
				condition := "1::domain_s.plan_each>0"
				s := fixture.Select(full(condition), []string{"lid", "rid"}, []string{"INT4", "INT4"}, nil)
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT nextval('domain_s.plan_steps'::regclass) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
				run(fixture.Exec("INSERT INTO domain_s.plan_l VALUES(1)", 1))
				run(fixture.Exec("INSERT INTO domain_s.plan_r VALUES(1)", 1))
				s = fixture.Select(full(condition), []string{"lid", "rid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}})
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT nextval('domain_s.plan_steps'::regclass) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
				run(domainCheckFailure(full("0::domain_s.plan_positive>0"), "plan_positive", "plan_positive_check", true, prepared))
				run(fixture.Exec("CREATE SEQUENCE domain_s.merge_steps START 1", 0))
				run(fixture.Exec("CREATE DOMAIN domain_s.merge_each AS integer CONSTRAINT merge_each_check CHECK(nextval('domain_s.merge_steps'::regclass)>0 AND VALUE>0)", 0))
				condition = "1::domain_s.merge_each>0"
				s = fixture.Exec(merge(condition), 0)
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT nextval('domain_s.merge_steps'::regclass) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
				run(fixture.Exec("INSERT INTO domain_s.merge_l VALUES(1)", 1))
				run(fixture.Exec("INSERT INTO domain_s.merge_r VALUES(1)", 1))
				run(domainCheckFailure(merge("0::domain_s.plan_positive>0"), "plan_positive", "plan_positive_check", false, prepared))
				run(fixture.Select("SELECT id FROM domain_s.merge_l", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				// BY SOURCE MERGE consumes ON at candidate matching and again
				// at action classification. Neither is a planning-side draw.
				s = fixture.Exec(merge(condition), 1)
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT nextval('domain_s.merge_steps'::regclass) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(4)}}))
				run(fixture.Select("SELECT id FROM domain_s.merge_l", []string{"id"}, []string{"INT4"}, nil))
			})
		})
	}
}
