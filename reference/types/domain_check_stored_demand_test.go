package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckStoredDemand(t *testing.T) {
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
				run(fixture.Exec("CREATE DOMAIN domain_s.stored_positive AS integer CONSTRAINT stored_positive_check CHECK(VALUE>0)", 0))
				run(fixture.Exec("CREATE DOMAIN domain_s.stored_recipe AS integer CHECK(1/0>0)", 0))
				// Catalog admission is not execution of a generated value.
				run(fixture.Exec("CREATE TABLE domain_s.stored_generated(id integer,v integer GENERATED ALWAYS AS(COALESCE((id::domain_s.stored_positive)::integer,1)) STORED)", 0))
				run(fixture.Exec("INSERT INTO domain_s.stored_generated(id) SELECT v FROM public.one WHERE false", 0))
				run(fixture.Exec("INSERT INTO domain_s.stored_generated(id) VALUES(1)", 1))
				run(domainCheckFailure("INSERT INTO domain_s.stored_generated(id) VALUES(0)", "stored_positive", "stored_positive_check", false, prepared))
				run(fixture.Select("SELECT id,v FROM domain_s.stored_generated", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
				// Generated-definition immutability admission initializes the
				// applicable CHECK recipes before any generated row is consumed.
				s := fixture.ErrorDiagnostic("CREATE TABLE domain_s.stored_generated_recipe(id integer,v integer GENERATED ALWAYS AS(COALESCE((id::domain_s.stored_recipe)::integer,1)) STORED)", fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
				s.ErrorStage = "execute"
				run(s)
				// Reusing the exact name proves the failed declaration published
				// no partial table; continue with an ordinary valid definition.
				run(fixture.Exec("CREATE TABLE domain_s.stored_generated_recipe(id integer,v integer GENERATED ALWAYS AS(id+1) STORED)", 0))
				run(fixture.Exec("INSERT INTO domain_s.stored_generated_recipe(id) SELECT v FROM public.one WHERE false", 0))
				run(fixture.Exec("INSERT INTO domain_s.stored_generated_recipe(id) VALUES(1)", 1))
				run(fixture.Select("SELECT id,v FROM domain_s.stored_generated_recipe", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
				// Keep the partition key's admitted physical integer provider.
				run(fixture.Exec("CREATE TABLE domain_s.stored_partition(id integer) PARTITION BY RANGE(((id::domain_s.stored_positive)::integer))", 0))
				run(fixture.Exec("CREATE TABLE domain_s.stored_partition_child PARTITION OF domain_s.stored_partition FOR VALUES FROM(1) TO(10)", 0))
				run(fixture.Exec("INSERT INTO domain_s.stored_partition SELECT v FROM public.one WHERE false", 0))
				run(fixture.Exec("INSERT INTO domain_s.stored_partition VALUES(1)", 1))
				run(domainCheckFailure("INSERT INTO domain_s.stored_partition VALUES(0)", "stored_positive", "stored_positive_check", false, prepared))
				run(fixture.Select("SELECT id FROM domain_s.stored_partition", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				s = fixture.ErrorDiagnostic("CREATE TABLE domain_s.stored_partition_recipe(id integer) PARTITION BY RANGE(((id::domain_s.stored_recipe)::integer))", fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Exec("CREATE TABLE domain_s.stored_partition_recipe(id integer) PARTITION BY RANGE(id)", 0))
				run(fixture.Exec("CREATE TABLE domain_s.stored_partition_recipe_child PARTITION OF domain_s.stored_partition_recipe FOR VALUES FROM(1) TO(10)", 0))
				run(fixture.Exec("INSERT INTO domain_s.stored_partition_recipe SELECT v FROM public.one WHERE false", 0))
				run(fixture.Exec("INSERT INTO domain_s.stored_partition_recipe VALUES(1)", 1))
				run(fixture.Select("SELECT id FROM domain_s.stored_partition_recipe", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
}
