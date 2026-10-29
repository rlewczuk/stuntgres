package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterConstraintDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, definition string }{
		{"nn", "NOT NULL a"}, {"ck", "CHECK(a>0)"}, {"uq", "UNIQUE(a)"}, {"pk", "PRIMARY KEY(a)"}, {"ex", "EXCLUDE USING btree(a WITH =)"}, {"fk", "FOREIGN KEY(a) REFERENCES p"}, {"missing", ""},
	} {
		for _, property := range []string{"", "DEFERRABLE", "INITIALLY DEFERRED", "ENFORCED", "NOT ENFORCED", "INHERIT", "NO INHERIT"} {
			if tc.name == "fk" && property != "INHERIT" && property != "NO INHERIT" && property != "" {
				continue
			}
			if tc.name == "nn" && (property == "INHERIT" || property == "NO INHERIT") {
				continue
			}
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/prepared_%t", tc.name, property, prepared), func(t *testing.T) {
					constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := enforcementRun(t, p, b, c)
						run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
						run(fixture.Exec("CREATE TABLE t(a INT)", 0))
						if tc.definition != "" {
							run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT "+tc.name+" "+tc.definition, 0))
						}
						before := constraintModeSnapshot(t, p, b, c)
						q := "ALTER TABLE t ALTER CONSTRAINT " + tc.name
						if property != "" {
							q += " " + property
						}
						var step fixture.Step
						if tc.name == "missing" {
							step = fixture.ErrorDiagnostic(q, constraintModeMissing("t", "missing"))
						} else if property == "" {
							step = fixture.Exec(q, 0)
						} else {
							message := `constraint "` + tc.name + `" of relation "t" is not a foreign key constraint`
							if property == "ENFORCED" || property == "NOT ENFORCED" {
								message = `cannot alter enforceability of constraint "` + tc.name + `" of relation "t"`
							}
							if property == "INHERIT" || property == "NO INHERIT" {
								message = `constraint "` + tc.name + `" of relation "t" is not a not-null constraint`
							}
							step = fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42809", message))
						}
						if prepared {
							step = fixture.Prepared(step)
							if property != "" || tc.name == "missing" {
								step.ErrorStage = "execute"
							}
						}
						run(step)
						constraintModeUnchanged(t, p, b, c, before)
					})
				})
			}
		}
	}
	for _, tc := range []struct{ clause, code, message, token string }{
		{"DEFERRABLE NOT DEFERRABLE", "42601", "conflicting constraint properties", "NOT DEFERRABLE"},
		{"NOT DEFERRABLE DEFERRABLE", "42601", "conflicting constraint properties", "DEFERRABLE"},
		{"INITIALLY IMMEDIATE INITIALLY DEFERRED", "42601", "conflicting constraint properties", "INITIALLY DEFERRED"},
		{"ENFORCED NOT ENFORCED", "42601", "conflicting constraint properties", "NOT ENFORCED"},
		{"NOT DEFERRABLE INITIALLY DEFERRED", "42601", "constraint declared INITIALLY DEFERRED must be DEFERRABLE", "INITIALLY DEFERRED"},
		{"NOT VALID", "0A000", "constraints cannot be altered to be NOT VALID", "NOT VALID"},
		{"NO INHERIT NOT VALID", "0A000", "constraints cannot be altered to be NOT VALID", "NO INHERIT"},
		{"INHERIT DEFERRABLE", "42601", `syntax error at or near "DEFERRABLE"`, "DEFERRABLE"},
		{"NO INHERIT garbage", "42601", `syntax error at or near "garbage"`, "garbage"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("syntax/%s/prepared_%t", tc.clause, prepared), func(t *testing.T) {
				constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := enforcementRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL)", 0))
					before := constraintModeSnapshot(t, p, b, c)
					q := "ALTER TABLE t ALTER CONSTRAINT nn " + tc.clause
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Position = enforcementPosition(q, tc.token, true)
					phase := "execute"
					if prepared {
						phase = "prepare"
					}
					addConstraintError(t, p, c, q, d, prepared, phase)
					constraintModeUnchanged(t, p, b, c, before)
				})
			})
		}
	}
	t.Run("equal_properties_mixed_precedence_and_quoted_names", func(t *testing.T) {
		constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec(`CREATE TABLE t(a INT CONSTRAINT "inherit" NOT NULL,CONSTRAINT fk FOREIGN KEY(a) REFERENCES p)`, 0))
			for _, q := range []string{"ALTER TABLE t ALTER CONSTRAINT fk DEFERRABLE DEFERRABLE INITIALLY DEFERRED INITIALLY DEFERRED", `ALTER TABLE t ALTER CONSTRAINT "inherit" NO INHERIT NO INHERIT`, `ALTER TABLE t ALTER CONSTRAINT "inherit" INHERIT`} {
				run(fixture.Prepared(fixture.Exec(q, 0)))
			}
			run(constraintModeNN("t", "inherit", false, true, 0, true))
			for _, tc := range []struct{ clause, message string }{
				{"NO INHERIT DEFERRABLE", `constraint "inherit" of relation "t" is not a foreign key constraint`},
				{"NO INHERIT NOT ENFORCED", `cannot alter enforceability of constraint "inherit" of relation "t"`},
			} {
				before := constraintModeSnapshot(t, p, b, c)
				run(fixture.ErrorDiagnostic(`ALTER TABLE t ALTER CONSTRAINT "inherit" `+tc.clause, fixture.ControlsReferenceDiagnostic("42809", tc.message)))
				constraintModeUnchanged(t, p, b, c, before)
			}
			q := `ALTER TABLE t ALTER CONSTRAINT "inherit" NO INHERIT; INSERT INTO t VALUES(7); garbage`
			d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "garbage"`)
			d.Position = enforcementPosition(q, "garbage", false)
			before := constraintModeSnapshot(t, p, b, c)
			run(fixture.ErrorDiagnostic(q, d))
			constraintModeUnchanged(t, p, b, c, before)
		})
	})
	t.Run("partition_and_inherited_restrictions", func(t *testing.T) {
		constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			constraintModeGraph(run)
			run(fixture.ErrorDiagnostic("ALTER TABLE child ALTER CONSTRAINT nn NO INHERIT", fixture.ControlsReferenceDiagnostic("55000", `cannot alter inherited constraint "nn" on relation "child"`)))
			before := constraintModeSnapshot(t, p, b, c)
			run(fixture.Exec("ALTER TABLE child ALTER CONSTRAINT nn INHERIT", 0))
			constraintModeUnchanged(t, p, b, c, before)
			run(fixture.Exec("CREATE TABLE pt(a INT CONSTRAINT pnn NOT NULL) PARTITION BY RANGE(a)", 0))
			run(fixture.Exec("CREATE TABLE part PARTITION OF pt FOR VALUES FROM(0) TO(10)", 0))
			run(fixture.ErrorDiagnostic("ALTER TABLE pt ALTER CONSTRAINT pnn NO INHERIT", fixture.ControlsReferenceDiagnostic("0A000", `not-null constraint "pnn" on partitioned table "pt" cannot be NO INHERIT`)))
			d := fixture.ControlsReferenceDiagnostic("42P16", "constraint must be altered in child tables too")
			d.Hint = "Do not specify the ONLY keyword."
			run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE ONLY pt ALTER CONSTRAINT pnn INHERIT", d)))
			run(fixture.ErrorDiagnostic("ALTER TABLE part ALTER CONSTRAINT pnn NO INHERIT", fixture.ControlsReferenceDiagnostic("55000", `cannot alter inherited constraint "pnn" on relation "part"`)))
		})
	})
}
