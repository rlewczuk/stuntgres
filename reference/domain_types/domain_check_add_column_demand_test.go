package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckAddColumnDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) {
					if !t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) }) {
						t.FailNow()
					}
				}
				alter := func(q string) { s := fixture.Exec(q, 0); s.Prepared = prepared; run(s) }
				run(fixture.Exec("CREATE DOMAIN domain_s.add_required AS integer CONSTRAINT add_required_check CHECK(VALUE IS NOT NULL)", 0))
				run(fixture.Exec("CREATE DOMAIN domain_s.add_positive AS integer CONSTRAINT add_positive_check CHECK(VALUE>0)", 0))
				run(fixture.Exec("CREATE TABLE domain_s.add_empty(id integer)", 0))
				alter("ALTER TABLE domain_s.add_empty ADD COLUMN v domain_s.add_required")
				run(fixture.Select("SELECT id,v FROM domain_s.add_empty", []string{"id", "v"}, []string{"INT4", "INT4"}, nil))
				run(domainCheckFailure("INSERT INTO domain_s.add_empty(id) VALUES(1)", "add_required", "add_required_check", false, prepared))
				run(fixture.Select("SELECT id,v FROM domain_s.add_empty", []string{"id", "v"}, []string{"INT4", "INT4"}, nil))
				run(fixture.Exec("CREATE TABLE domain_s.add_seeded(id integer)", 0))
				run(fixture.Exec("INSERT INTO domain_s.add_seeded VALUES(1),(2)", 2))
				run(domainCheckFailure("ALTER TABLE domain_s.add_seeded ADD COLUMN v domain_s.add_required", "add_required", "add_required_check", false, prepared))
				run(fixture.Select("SELECT id FROM domain_s.add_seeded ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				alter("ALTER TABLE domain_s.add_seeded ADD COLUMN v domain_s.add_required DEFAULT 1")
				run(fixture.Select("SELECT id,v FROM domain_s.add_seeded ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}}))
				run(fixture.Exec("CREATE TABLE domain_s.add_zero_empty(id integer)", 0))
				alter("ALTER TABLE domain_s.add_zero_empty ADD COLUMN v domain_s.add_positive DEFAULT 0")
				run(fixture.Select("SELECT id,v FROM domain_s.add_zero_empty", []string{"id", "v"}, []string{"INT4", "INT4"}, nil))
				run(domainCheckFailure("INSERT INTO domain_s.add_zero_empty(id) VALUES(1)", "add_positive", "add_positive_check", false, prepared))
				run(fixture.Exec("CREATE TABLE domain_s.add_zero_seeded(id integer)", 0))
				run(fixture.Exec("INSERT INTO domain_s.add_zero_seeded VALUES(1),(2)", 2))
				run(domainCheckFailure("ALTER TABLE domain_s.add_zero_seeded ADD COLUMN v domain_s.add_positive DEFAULT 0", "add_positive", "add_positive_check", false, prepared))
				run(fixture.Select("SELECT id FROM domain_s.add_zero_seeded ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				alter("ALTER TABLE domain_s.add_zero_seeded ADD COLUMN v domain_s.add_positive DEFAULT 1")
				run(fixture.Select("SELECT id,v FROM domain_s.add_zero_seeded ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}}))

				// Constrained domains cannot use a once-only missing-value cache.
				// SQL sequence state witnesses each actual existing/default row.
				run(fixture.Exec("CREATE SEQUENCE domain_s.add_steps START 1", 0))
				run(fixture.Exec("CREATE DOMAIN domain_s.add_each AS integer CONSTRAINT add_each_check CHECK(nextval('domain_s.add_steps'::regclass)>0 AND VALUE>0)", 0))
				run(fixture.Exec("CREATE TABLE domain_s.add_many(id integer)", 0))
				run(fixture.Exec("INSERT INTO domain_s.add_many VALUES(1),(2),(3)", 3))
				alter("ALTER TABLE domain_s.add_many ADD COLUMN v domain_s.add_each DEFAULT 1")
				run(fixture.Select("SELECT id,v FROM domain_s.add_many ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}, {int64(3), int64(1)}}))
				run(fixture.Select("SELECT nextval('domain_s.add_steps'::regclass) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(4)}}))
				run(fixture.Exec("CREATE SEQUENCE domain_s.add_empty_steps START 1", 0))
				run(fixture.Exec("CREATE DOMAIN domain_s.add_empty_each AS integer CONSTRAINT add_empty_each_check CHECK(nextval('domain_s.add_empty_steps'::regclass)>0 AND VALUE>0)", 0))
				run(fixture.Exec("CREATE TABLE domain_s.add_draw_empty(id integer)", 0))
				alter("ALTER TABLE domain_s.add_draw_empty ADD COLUMN v domain_s.add_empty_each DEFAULT 1")
				run(fixture.Select("SELECT nextval('domain_s.add_empty_steps'::regclass) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
				var s = fixture.Exec("INSERT INTO domain_s.add_draw_empty(id) VALUES(1),(2)", 2)
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT id,v FROM domain_s.add_draw_empty ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}}))
				run(fixture.Select("SELECT nextval('domain_s.add_empty_steps'::regclass) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(4)}}))
				run(fixture.Exec("CREATE DOMAIN domain_s.add_recipe AS integer CONSTRAINT a_false CHECK(FALSE) CONSTRAINT z_error CHECK(1/0>0)", 0))
				run(fixture.Exec("CREATE TABLE domain_s.add_recipe_empty(id integer)", 0))
				d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				s = fixture.ErrorDiagnostic("ALTER TABLE domain_s.add_recipe_empty ADD COLUMN v domain_s.add_recipe DEFAULT 1", d)
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				alter("ALTER TABLE domain_s.add_recipe_empty ADD COLUMN v integer DEFAULT 9")
				run(fixture.Select("SELECT id,v FROM domain_s.add_recipe_empty", []string{"id", "v"}, []string{"INT4", "INT4"}, nil))
			})
		})
	}
}
