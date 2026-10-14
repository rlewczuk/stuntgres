package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckIndexInsertionOrderDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, returning := range []bool{false, true} {
			for _, firstFails := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared_%t/returning_%t/first_fails_%t", prepared, returning, firstFails), func(t *testing.T) {
					domainCheckIndexEventScenario(t, prepared, func(_ *testing.T, _ *fixture.Pair, _ fixture.Backend, _ *sql.Conn, run func(fixture.Step)) {
						run(fixture.Exec("CREATE SEQUENCE domain_s.ordered_index_steps", 0))
						comparison := "<"
						if firstFails {
							comparison = ">"
						}
						run(fixture.Exec("CREATE DOMAIN domain_s.ordered_first AS integer CONSTRAINT first_failure CHECK(VALUE"+comparison+"nextval('domain_s.ordered_index_steps'))", 0))
						run(fixture.Exec("CREATE DOMAIN domain_s.ordered_second AS integer CONSTRAINT second_failure CHECK(VALUE>nextval('domain_s.ordered_index_steps'))", 0))
						run(fixture.Exec("CREATE TABLE domain_s.ordered_index_target(id integer)", 0))
						// Reverse lexical names distinguish creation/OID order from
						// alphabetical index or domain/constraint ordering.
						run(fixture.Exec("CREATE INDEX z_first_created ON domain_s.ordered_index_target (((id::domain_s.ordered_first)::integer))", 0))
						run(fixture.Exec("CREATE INDEX a_second_created ON domain_s.ordered_index_target (((id::domain_s.ordered_second)::integer))", 0))
						run(fixture.Select("SELECT nextval('domain_s.ordered_index_steps') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
						query := "INSERT INTO domain_s.ordered_index_target VALUES(0)"
						if returning {
							query += " RETURNING id"
						}
						domain, constraint, effects := "ordered_second", "second_failure", int64(3)
						if firstFails {
							domain, constraint, effects = "ordered_first", "first_failure", 2
						}
						run(domainCheckFailure(query, domain, constraint, returning, prepared))
						run(fixture.Select("SELECT currval('domain_s.ordered_index_steps') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{effects}}))
						run(fixture.Select("SELECT id FROM domain_s.ordered_index_target", []string{"id"}, []string{"INT4"}, nil))
						// Same physical owner continues after the atomic failure;
						// sequences retain exactly the actual pre-error effects.
						run(fixture.Select("SELECT nextval('domain_s.ordered_index_steps') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{effects + 1}}))
					})
				})
			}
		}
	}
}
