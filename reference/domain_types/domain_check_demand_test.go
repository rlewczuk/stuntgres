package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Shared contracts distinguish expression preparation, runtime conversion truth,
// and genuine parameter input.
func TestReferenceDomainCheckDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				run(fixture.Exec("CREATE DOMAIN domain_s.demand_positive AS integer CONSTRAINT demand_positive_check CHECK(VALUE>0)", 0))
				run(fixture.Exec("CREATE DOMAIN domain_s.demand_required AS integer CONSTRAINT demand_required_check CHECK(VALUE IS NOT NULL)", 0))
				run(fixture.Exec("CREATE TABLE domain_s.empty_source(id integer)", 0))
				run(fixture.Exec("CREATE TABLE domain_s.demand_destination(id integer,v domain_s.demand_required)", 0))
				run(fixture.Exec("INSERT INTO domain_s.demand_destination VALUES(1,1)", 1))

				for _, q := range []string{
					"SELECT 0::domain_s.demand_positive AS v FROM public.one WHERE false",
					"SELECT 0::domain_s.demand_positive AS v FROM public.one WHERE v<0",
					"SELECT CAST(0 AS domain_s.demand_positive) AS v FROM domain_s.empty_source",
					"SELECT domain_s.demand_positive '0' AS v FROM domain_s.empty_source",
					"SELECT NULL::domain_s.demand_required AS v FROM public.one WHERE false",
				} {
					t.Run(q, func(t *testing.T) {
						s := fixture.Select(q, []string{"v"}, []string{"INT4"}, nil)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, r, s)
					})
				}
				for _, tc := range []struct {
					q     string
					input any
				}{
					{"SELECT $1::integer::domain_s.demand_positive AS v FROM public.one WHERE false", 0},
					{"SELECT $1::integer::domain_s.demand_required AS v FROM public.one WHERE false", nil},
				} {
					s := fixture.Select(tc.q, []string{"v"}, []string{"INT4"}, nil, tc.input)
					s.Prepared = prepared
					run(s)
				}
				// A typed NULL produced by an empty domain subquery is already a
				// domain datum, not fresh NULL input requiring another CHECK.
				s := fixture.Select("SELECT (SELECT NULL::domain_s.demand_required FROM public.one WHERE false)::domain_s.demand_required AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{nil}})
				s.Prepared = prepared
				run(s)
				for _, q := range []string{
					"INSERT INTO domain_s.demand_destination(id) SELECT v FROM public.one WHERE false",
					"INSERT INTO domain_s.demand_destination(id) SELECT id FROM domain_s.empty_source",
				} {
					s := fixture.Exec(q, 0)
					s.Prepared = prepared
					run(s)
				}
				run(fixture.Select("SELECT id,v FROM domain_s.demand_destination ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
				// When a row consumes the omitted input, the same fresh NULL
				// conversion must fail and leave the destination unchanged.
				run(domainCheckFailure("INSERT INTO domain_s.demand_destination(id) SELECT v+1 FROM public.one", "demand_required", "demand_required_check", false, prepared))
				run(fixture.Select("SELECT id,v FROM domain_s.demand_destination ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
				// Inferred domain parameters are input events before the false
				// consumer. An explicitly base-typed parameter above is not.
				for _, tc := range []struct {
					q, domain, constraint, context string
					input                          any
				}{
					{"SELECT $1::domain_s.demand_positive AS v FROM public.one WHERE false", "demand_positive", "demand_positive_check", "unnamed portal parameter $1 = '...'", 0},
					{"SELECT $1::domain_s.demand_required AS v FROM public.one WHERE false", "demand_required", "demand_required_check", "unnamed portal parameter $1", nil},
				} {
					d := domainCheckDiagnostic(tc.domain, tc.constraint)
					d.Context = tc.context
					s := fixture.ErrorDiagnostic(tc.q, d, tc.input)
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					run(s)
				}

				// CHECK recipes are prepared when the domain expression is
				// initialized, independently of whether its truth is consumed.
				run(fixture.Exec("CREATE DOMAIN domain_s.demand_recipe AS integer CONSTRAINT a_false CHECK(FALSE) CONSTRAINT z_error CHECK(1/0>0)", 0))
				run(fixture.Exec("CREATE TABLE domain_s.recipe_destination(id integer,v domain_s.demand_recipe)", 0))
				for _, tc := range []struct {
					q    string
					rows bool
				}{
					{"SELECT 1::domain_s.demand_recipe AS v FROM public.one WHERE false", true},
					{"SELECT 1::domain_s.demand_recipe AS v FROM domain_s.empty_source", true},
					{"INSERT INTO domain_s.recipe_destination(id) SELECT id FROM domain_s.empty_source", false},
				} {
					d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
					s := fixture.ErrorDiagnostic(tc.q, d)
					s.SelectRows, s.Prepared, s.ErrorStage = tc.rows, prepared, "execute"
					run(s)
				}
				run(fixture.Select("SELECT id,v FROM domain_s.recipe_destination", []string{"id", "v"}, []string{"INT4", "INT4"}, nil))

				// Observe real SQL sequence state, not an evaluator mock/counter.
				// Three consumed rows must each execute the volatile CHECK even
				// though its physical argument is the immutable constant 1.
				run(fixture.Exec("CREATE TABLE domain_s.three(id integer)", 0))
				run(fixture.Exec("INSERT INTO domain_s.three VALUES(1),(2),(3)", 3))
				run(fixture.Exec("CREATE SEQUENCE domain_s.demand_steps START 1", 0))
				run(fixture.Exec("CREATE DOMAIN domain_s.demand_each AS integer CONSTRAINT demand_each_check CHECK(nextval('domain_s.demand_steps'::regclass)>0 AND VALUE>0)", 0))
				s = fixture.Select("SELECT 1::domain_s.demand_each AS v FROM domain_s.three ORDER BY id", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}})
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT nextval('domain_s.demand_steps'::regclass) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(4)}}))
				run(fixture.Exec("CREATE SEQUENCE domain_s.limited_steps START 1", 0))
				run(fixture.Exec("CREATE DOMAIN domain_s.demand_limited AS integer CONSTRAINT demand_limited_check CHECK(nextval('domain_s.limited_steps'::regclass)<3 AND VALUE>0)", 0))
				run(fixture.Exec("CREATE TABLE domain_s.limited_destination(v domain_s.demand_limited)", 0))
				run(domainCheckFailure("INSERT INTO domain_s.limited_destination SELECT 1::domain_s.demand_limited FROM domain_s.three ORDER BY id", "demand_limited", "demand_limited_check", false, prepared))
				run(fixture.Select("SELECT v FROM domain_s.limited_destination", []string{"v"}, []string{"INT4"}, nil))
				run(fixture.Select("SELECT currval('domain_s.limited_steps'::regclass) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
			})
		})
	}
}
