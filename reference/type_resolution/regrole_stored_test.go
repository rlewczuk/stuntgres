package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRegroleStoredConstants(t *testing.T) {
	regroleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE rr_alter(r regrole DEFAULT NULL::regrole)", 0))
		for _, q := range []string{
			"CREATE TABLE rr_bad(r regrole DEFAULT 'rr_role'::regrole)",
			"CREATE TABLE rr_bad(r regrole DEFAULT 'rr_role')",
			"CREATE TABLE rr_bad(r regrole DEFAULT '0'::regrole)",
			"CREATE TABLE rr_bad(r regrole DEFAULT '-'::regrole)",
			"CREATE TABLE rr_bad(r regrole DEFAULT '4294967295'::regrole)",
			"ALTER TABLE rr_alter ALTER COLUMN r SET DEFAULT 'rr_role'::regrole",
			"ALTER TABLE rr_alter ADD COLUMN s regrole DEFAULT 'rr_role'::regrole",
			"CREATE TABLE rr_bad(r regrole CHECK(r='rr_role'::regrole))",
			"CREATE VIEW rr_bad_view AS SELECT 'rr_role'::regrole AS r",
			"CREATE MATERIALIZED VIEW rr_bad_view AS SELECT 'rr_role'::regrole AS r",
			"CREATE VIEW rr_bad_view AS SELECT 'rr_role'::regrole::oid AS r",
			"CREATE VIEW rr_bad_view AS SELECT 'rr_role'::regrole::text AS r",
			"CREATE VIEW rr_bad_view AS SELECT ARRAY['rr_role'::regrole] AS r",
			"CREATE VIEW rr_bad_view AS SELECT CASE WHEN true THEN 'rr_role'::regrole ELSE NULL::regrole END AS r",
		} {
			t.Run(q, func(t *testing.T) {
				want := fixture.ControlsReferenceDiagnostic("0A000", "constant of the type regrole cannot be used here")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, want))
			})
		}
		for _, q := range []string{
			"CREATE TABLE rr_ok(r regrole DEFAULT NULL::regrole)",
			"CREATE TABLE rr_late(r regrole DEFAULT 'rr_role'::text::regrole)",
			"CREATE VIEW rr_null AS SELECT NULL::regrole AS r",
			"CREATE VIEW rr_runtime AS SELECT 'rr_role'::text::regrole AS r",
			"CREATE VIEW rr_column AS SELECT r FROM rr_late",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, table := range []string{"rr_alter", "rr_ok", "rr_late"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO "+table+" DEFAULT VALUES", 1))
		}
		for _, table := range []string{"rr_alter", "rr_ok", "rr_null"} {
			regroleBoth(t, p, b, c, fixture.Select("SELECT r FROM "+table, []string{"r"}, []string{"REGROLE"}, [][]any{{nil}}))
		}
		for _, table := range []string{"rr_late", "rr_runtime", "rr_column"} {
			regroleBoth(t, p, b, c, fixture.Select("SELECT r FROM "+table, []string{"r"}, []string{"REGROLE"}, [][]any{{"rr_role"}}))
		}
	})
}

func TestReferenceRegroleStoredShapes(t *testing.T) {
	regroleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var bootstrap string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&bootstrap); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"CREATE DOMAIN rr_domain AS regrole", "CREATE DOMAIN rr_array_domain AS regrole[]", "CREATE TABLE rr_index(r regrole)", "CREATE TABLE rr_policy(r regrole)"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		shapes := []struct {
			expression, typ string
			value           any
			banned          bool
		}{
			{"10::regrole", "regrole", bootstrap, false}, {"10::bigint::regrole", "regrole", bootstrap, false}, {"10::oid::regrole", "regrole", bootstrap, false},
			{"'10'::oid::regrole", "regrole", bootstrap, false}, {"'rr_role'::text::regrole", "regrole", "rr_role", false}, {"NULL::regrole", "regrole", nil, false},
			{"'rr_role'::regrole", "regrole", nil, true}, {"'{rr_role}'::regrole[]", "regrole[]", "{rr_role}", false},
			{"ARRAY['rr_role'::regrole]", "regrole[]", nil, true}, {"ARRAY[10::regrole]", "regrole[]", "{" + bootstrap + "}", false},
			{"'rr_role'::rr_domain", "rr_domain", nil, true}, {"'rr_role'::regrole::rr_domain", "rr_domain", nil, true},
			{"'{rr_role}'::rr_array_domain", "rr_array_domain", "{rr_role}", false}, {"NULL::rr_domain", "rr_domain", nil, false},
		}
		for i, shape := range shapes {
			for _, consumer := range []string{"default", "view", "check"} {
				name := fmt.Sprintf("rr_shape_%d_%s", i, consumer)
				var q string
				switch consumer {
				case "default":
					q = fmt.Sprintf("CREATE TABLE %s(r %s DEFAULT %s)", name, shape.typ, shape.expression)
				case "view":
					q = fmt.Sprintf("CREATE VIEW %s AS SELECT %s AS r", name, shape.expression)
				case "check":
					q = fmt.Sprintf("CREATE TABLE %s(r %s CHECK(r=%s))", name, shape.typ, shape.expression)
				}
				t.Run(name, func(t *testing.T) {
					if !b.PG && consumer == "check" && (shape.typ == "regrole[]" || shape.typ == "rr_array_domain") {
						// Native array-to-array equality is a broader capability
						// boundary, reached before this consumer's Const walk.
						want := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+shape.typ+" = "+shape.typ)
						want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
						want.Position = fixture.Position(strings.Index(q, "=") + 1)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, want))
						return
					}
					if shape.banned {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "constant of the type regrole cannot be used here")))
						return
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
					if consumer == "default" {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO "+name+" DEFAULT VALUES", 1))
					}
					if consumer != "check" {
						var value any
						if err := c.QueryRowContext(p.Ctx, "SELECT r FROM "+name).Scan(&value); err != nil {
							t.Fatal(err)
						}
						if fixture.Normalize(value) != shape.value {
							t.Errorf("stored shape value=%#v want %#v", fixture.Normalize(value), shape.value)
						}
						if value != nil {
							if b.PG {
								if _, ok := value.([]byte); !ok {
									t.Errorf("PG raw %T want []byte", value)
								}
							} else if _, ok := value.(string); !ok {
								t.Errorf("native raw %T want string", value)
							}
						}
					}
				})
			}
		}
		for _, q := range []string{
			"CREATE TABLE rr_generated(r regrole GENERATED ALWAYS AS ('rr_role'::regrole) STORED)",
			"CREATE TABLE rr_generated_runtime(r regrole GENERATED ALWAYS AS ('rr_role'::text::regrole) STORED)",
			"CREATE INDEX rr_expr ON rr_index (('rr_role'::regrole))",
			"CREATE INDEX rr_partial ON rr_index(r) WHERE r='rr_role'::regrole",
			"CREATE DOMAIN rr_checked AS regrole CHECK(VALUE='rr_role'::regrole)",
			"CREATE POLICY rr_policy_const ON rr_policy USING(r='rr_role'::regrole)",
			"CREATE TABLE rr_missing(r regrole DEFAULT 'rr_absent'::regrole)",
			"ALTER TABLE rr_index ALTER COLUMN r SET DEFAULT 'rr_absent'::regrole",
		} {
			t.Run(q, func(t *testing.T) {
				want := fixture.ControlsReferenceDiagnostic("0A000", "constant of the type regrole cannot be used here")
				switch q {
				case "CREATE DOMAIN rr_checked AS regrole CHECK(VALUE='rr_role'::regrole)":
					if !b.PG {
						want = fixture.ControlsReferenceDiagnostic("0A000", "domain constraints and defaults are unsupported")
						want.Position = "37"
					}
				case "CREATE TABLE rr_generated_runtime(r regrole GENERATED ALWAYS AS ('rr_role'::text::regrole) STORED)":
					want = fixture.ControlsReferenceDiagnostic("42P17", "generation expression is not immutable")
				case "CREATE TABLE rr_missing(r regrole DEFAULT 'rr_absent'::regrole)":
					want = fixture.ControlsReferenceDiagnostic("42704", `role "rr_absent" does not exist`)
					want.Position = "43"
				case "ALTER TABLE rr_index ALTER COLUMN r SET DEFAULT 'rr_absent'::regrole":
					want = fixture.ControlsReferenceDiagnostic("42704", `role "rr_absent" does not exist`)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, want))
			})
		}
	})
}
