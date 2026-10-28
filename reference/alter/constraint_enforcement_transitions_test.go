package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConstraintEnforcementTransitions(t *testing.T) {
	for _, kind := range []string{"check", "fk"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/held_%v", kind, prepared), func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := addConstraintSetup(t, p, b, c)
					name, clause, def, q, bad := "ck", "CHECK(a>0)", "CHECK ((a > 0))", "ALTER TABLE t DROP CONSTRAINT ck, ADD CONSTRAINT ck CHECK(a>0) ENFORCED", 0
					if kind == "fk" {
						name, clause, def, q, bad = "fk", "FOREIGN KEY(a) REFERENCES p(id)", "FOREIGN KEY (a) REFERENCES p(id)", "ALTER TABLE t ALTER CONSTRAINT fk ENFORCED", 9
					}
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT "+name+" "+clause+" NOT ENFORCED", 0))
					run(fixture.Exec("INSERT INTO t VALUES($1,'bad')", 1, bad))
					oid := enforcementIdentity(t, p, b, c, "t", name, def+" NOT ENFORCED", false, false)
					before := enforcementSnapshot(t, p, b, c)
					held := addConstraintHeld(t, p, b, c, q)
					enforcementUnchanged(t, p, b, c, before)
					execute := run
					if prepared {
						execute = func(s fixture.Step) { validateConstraintHeldRun(t, p, b, held, s) }
					}
					d := enforcementCheck("t", name, true, "")
					if kind == "fk" {
						d = addConstraintFK("t", name, "a", "9", "p")
					}
					execute(fixture.ErrorDiagnostic(q, d))
					enforcementUnchanged(t, p, b, c, before)
					run(fixture.Exec("UPDATE t SET a=1", 1))
					execute(fixture.Exec(q, 0))
					next := enforcementIdentity(t, p, b, c, "t", name, def, true, true)
					if kind == "check" {
						if next == oid {
							t.Fatal("CHECK replacement retained original identity")
						}
						run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(0,'bad')", enforcementCheck("t", "ck", false, "0, bad")))
					} else {
						if next != oid {
							t.Fatal("FK toggle changed constraint identity", oid, next)
						}
						triggers := enforcementTriggers(t, p, b, c, oid, true)
						stable := enforcementSnapshot(t, p, b, c)
						execute(fixture.Exec(q, 0))
						enforcementUnchanged(t, p, b, c, stable)
						run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(9,'bad')", addConstraintFK("t", "fk", "a", "9", "p")))
						run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT fk NOT ENFORCED", 0))
						enforcementIdentity(t, p, b, c, "t", "fk", def+" NOT ENFORCED", false, false)
						enforcementTriggers(t, p, b, c, oid, false)
						execute(fixture.Exec(q, 0))
						enforcementNewTriggers(t, triggers, enforcementTriggers(t, p, b, c, oid, true))
					}
					run(addConstraintRows([]any{int64(1), "bad"}))
				})
			})
		}
	}
}

// Each matrix entry is a separate scenario/backend leaf: native syntax failure
// cannot prevent any PostgreSQL case from running. These expectations follow
// REL_18_6 tablecmds.c's ADD pass, ordered MISC pass and queued validation work;
// mixed toggle/drop scheduling remains explicitly subject to the root oracle gate.
func TestReferenceConstraintEnforcementMixedActions(t *testing.T) {
	for _, tc := range []struct {
		name, initial, actions, errorName, errorColumn, errorValue string
		finalEnforced, finalValid                                  bool
	}{
		{"enable_disable_bad", "NOT ENFORCED", "ALTER CONSTRAINT fk ENFORCED, ALTER CONSTRAINT fk NOT ENFORCED", "fk", "a", "9", false, false},
		{"enable_disable_clean", "NOT ENFORCED", "ALTER CONSTRAINT fk ENFORCED, ALTER CONSTRAINT fk NOT ENFORCED", "", "", "", false, false},
		{"disable_enable_invalid", "NOT VALID", "ALTER CONSTRAINT fk NOT ENFORCED, ALTER CONSTRAINT fk ENFORCED", "fk", "a", "9", true, true},
		{"enable_validate", "NOT ENFORCED", "ALTER CONSTRAINT fk ENFORCED, VALIDATE CONSTRAINT fk", "fk", "a", "9", true, true},
		{"repeat_enable", "NOT ENFORCED", "ALTER CONSTRAINT fk ENFORCED, ALTER CONSTRAINT fk ENFORCED", "fk", "a", "9", true, true},
		{"repeat_disable", "NOT ENFORCED", "ALTER CONSTRAINT fk NOT ENFORCED, ALTER CONSTRAINT fk NOT ENFORCED", "", "", "", false, false},
		{"shape_companion", "NOT ENFORCED", "ADD COLUMN more INT, ALTER COLUMN a TYPE BIGINT, ALTER CONSTRAINT fk ENFORCED", "fk", "a", "9", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				value := 1
				if tc.errorName != "" {
					value = 9
				}
				run(fixture.Exec("INSERT INTO t VALUES($1,'history')", 1, value))
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(id) "+tc.initial, 0))
				var oid int64
				if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conrelid='t'::regclass AND conname='fk'").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				before := enforcementSnapshot(t, p, b, c)
				held := addConstraintHeld(t, p, b, c, "ALTER TABLE t "+tc.actions)
				if tc.errorName != "" {
					validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, addConstraintFK("t", tc.errorName, tc.errorColumn, tc.errorValue, "p")))
					enforcementUnchanged(t, p, b, c, before)
					run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
				}
				validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
				run(enforcementFlags("t", "fk", tc.finalEnforced, tc.finalValid))
				definition := "FOREIGN KEY (a) REFERENCES p(id)"
				if !tc.finalEnforced {
					definition += " NOT ENFORCED"
				}
				current := enforcementIdentity(t, p, b, c, "t", "fk", definition, tc.finalEnforced, tc.finalValid)
				if tc.name == "shape_companion" {
					if current == oid {
						t.Fatal("ALTER TYPE retained the rebuilt FK identity", oid, current)
					}
					run(fixture.Select("SELECT oid FROM pg_constraint WHERE oid=$1", []string{"oid"}, []string{"OID"}, nil, oid))
				} else if current != oid {
					t.Fatal("ordinary enforcement toggle changed FK identity", oid, current)
				}
				enforcementTriggers(t, p, b, c, current, tc.finalEnforced)
				if tc.name == "shape_companion" {
					run(fixture.Select("SELECT a,note,more FROM t", []string{"a", "note", "more"}, []string{"INT8", "TEXT", "INT4"}, [][]any{{int64(9), "history", nil}}))
				}
			})
		})
	}
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("misc_order/reverse_%v", reverse), func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := enforcementRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE t(a INT,b INT,CONSTRAINT fk_a FOREIGN KEY(a) REFERENCES p NOT ENFORCED,CONSTRAINT fk_b FOREIGN KEY(b) REFERENCES p NOT ENFORCED)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(9,10)", 1))
				first, second, column, value, repair := "fk_b", "fk_a", "b", "10", 10
				if reverse {
					first, second, column, value, repair = "fk_a", "fk_b", "a", "9", 9
				}
				held := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT "+first+" ENFORCED, ALTER CONSTRAINT "+second+" ENFORCED")
				before := enforcementSnapshot(t, p, b, c)
				validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, addConstraintFK("t", first, column, value, "p")))
				enforcementUnchanged(t, p, b, c, before)
				run(fixture.Exec("INSERT INTO p VALUES($1)", 1, repair))
				column, value, repair = "a", "9", 9
				if reverse {
					column, value, repair = "b", "10", 10
				}
				before = enforcementSnapshot(t, p, b, c)
				validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, addConstraintFK("t", second, column, value, "p")))
				enforcementUnchanged(t, p, b, c, before)
				run(fixture.Exec("INSERT INTO p VALUES($1)", 1, repair))
				validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
				run(enforcementFlags("t", "fk_a", true, true))
				run(enforcementFlags("t", "fk_b", true, true))
			})
		})
		for _, scenario := range []string{"add_enable", "add_before_misc"} {
			t.Run(fmt.Sprintf("%s/reverse_%v", scenario, reverse), func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := addConstraintSetup(t, p, b, c)
					run(fixture.Exec("INSERT INTO t VALUES(9,'history')", 1))
					first, second := "ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED", "ALTER CONSTRAINT fk ENFORCED"
					if scenario == "add_before_misc" {
						run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT old_fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED", 0))
						first, second = "ADD CONSTRAINT added FOREIGN KEY(a) REFERENCES p", "ALTER CONSTRAINT old_fk ENFORCED"
					}
					if reverse {
						first, second = second, first
					}
					held := addConstraintHeld(t, p, b, c, "ALTER TABLE t "+first+", "+second)
					name := "fk"
					if scenario == "add_before_misc" {
						name = "added"
					}
					before := enforcementSnapshot(t, p, b, c)
					validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, addConstraintFK("t", name, "a", "9", "p")))
					enforcementUnchanged(t, p, b, c, before)
					run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
					validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
					run(enforcementFlags("t", name, true, true))
				})
			})
		}
	}
	for _, actions := range []string{"ALTER CONSTRAINT fk NOT ENFORCED, VALIDATE CONSTRAINT fk", "ALTER CONSTRAINT fk ENFORCED, VALIDATE CONSTRAINT ck"} {
		t.Run("late_failure/"+actions, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED, ADD CONSTRAINT ck CHECK(a>0) NOT ENFORCED", 0))
				before := enforcementSnapshot(t, p, b, c)
				run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE t "+actions, fixture.ControlsReferenceDiagnostic("55000", "cannot validate NOT ENFORCED constraint"))))
				enforcementUnchanged(t, p, b, c, before)
			})
		})
	}
	// DROP runs before MISC: the held name resolves to the replacement, never
	// to an old same-name queued identity. Root must confirm this oracle edge.
	t.Run("drop_readd_identity", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED", 0))
			old := enforcementIdentity(t, p, b, c, "t", "fk", "FOREIGN KEY (a) REFERENCES p(id) NOT ENFORCED", false, false)
			run(fixture.Exec("INSERT INTO t VALUES(9,'bad')", 1))
			q := "ALTER TABLE t ALTER CONSTRAINT fk ENFORCED, DROP CONSTRAINT fk, ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED"
			before := enforcementSnapshot(t, p, b, c)
			run(fixture.ErrorDiagnostic(q, addConstraintFK("t", "fk", "a", "9", "p")))
			enforcementUnchanged(t, p, b, c, before)
			run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
			run(fixture.Exec(q, 0))
			next := enforcementIdentity(t, p, b, c, "t", "fk", "FOREIGN KEY (a) REFERENCES p(id)", true, true)
			if next == old {
				t.Fatal("DROP/ADD reused identity")
			}
		})
	})
}

// REL_18_6 queues ADD and VALIDATE scans before Phase 3; a later disable
// changes final metadata, not the historical obligation of those queued scans.
func TestReferenceConstraintEnforcementQueuedHistory(t *testing.T) {
	for _, origin := range []string{"add", "add_textually_last", "validate"} {
		for _, history := range []string{"empty", "valid", "invalid"} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/held_%v", origin, history, prepared), func(t *testing.T) {
					addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := addConstraintSetup(t, p, b, c)
						if history != "empty" {
							value := 1
							if history == "invalid" {
								value = 9
							}
							run(fixture.Exec("INSERT INTO t VALUES($1,'history')", 1, value))
						}
						var old int64
						actions := "ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p,ALTER CONSTRAINT fk NOT ENFORCED"
						if origin == "add_textually_last" {
							actions = "ALTER CONSTRAINT fk NOT ENFORCED,ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p"
						}
						if origin == "validate" {
							run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID", 0))
							old = enforcementIdentity(t, p, b, c, "t", "fk", "FOREIGN KEY (a) REFERENCES p(id) NOT VALID", true, false)
							actions = "VALIDATE CONSTRAINT fk,ALTER CONSTRAINT fk NOT ENFORCED"
						}
						beforePrepare := enforcementSnapshot(t, p, b, c)
						held := addConstraintHeld(t, p, b, c, "ALTER TABLE t "+actions)
						enforcementUnchanged(t, p, b, c, beforePrepare)
						execute := run
						if prepared {
							execute = func(step fixture.Step) { validateConstraintHeldRun(t, p, b, held, step) }
						}
						if history == "invalid" {
							before := enforcementSnapshot(t, p, b, c)
							execute(fixture.ErrorDiagnostic(held.SQL, addConstraintFK("t", "fk", "a", "9", "p")))
							enforcementUnchanged(t, p, b, c, before)
							run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
						}
						execute(fixture.Exec(held.SQL, 0))
						current := enforcementIdentity(t, p, b, c, "t", "fk", "FOREIGN KEY (a) REFERENCES p(id) NOT ENFORCED", false, false)
						if origin == "validate" && current != old {
							t.Fatal("VALIDATE/disable replaced FK identity", old, current)
						}
						enforcementTriggers(t, p, b, c, current, false)
						run(fixture.Exec("INSERT INTO t VALUES(11,'future')", 1))
						rows := [][]any{{int64(11), "future"}}
						if history == "valid" {
							rows = append([][]any{{int64(1), "history"}}, rows...)
						}
						if history == "invalid" {
							rows = append([][]any{{int64(9), "history"}}, rows...)
						}
						run(fixture.Select("SELECT a,note FROM t ORDER BY a", []string{"a", "note"}, []string{"INT4", "TEXT"}, rows))
					})
				})
			}
		}
	}
	for _, route := range []string{"add_before_misc", "misc_b_then_a", "misc_a_then_b"} {
		t.Run("first_error/"+route, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := enforcementRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(9,10)", 1))
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk_b FOREIGN KEY(b) REFERENCES p NOT VALID", 0))
				first, second, firstColumn, secondColumn, firstValue, secondValue := "fk_b", "fk_a", "b", "a", 10, 9
				actions := "VALIDATE CONSTRAINT fk_b,ALTER CONSTRAINT fk_b NOT ENFORCED,VALIDATE CONSTRAINT fk_a,ALTER CONSTRAINT fk_a NOT ENFORCED"
				if route == "add_before_misc" {
					actions = "VALIDATE CONSTRAINT fk_b,ALTER CONSTRAINT fk_b NOT ENFORCED,ADD CONSTRAINT fk_a FOREIGN KEY(a) REFERENCES p,ALTER CONSTRAINT fk_a NOT ENFORCED"
				} else {
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk_a FOREIGN KEY(a) REFERENCES p NOT VALID", 0))
				}
				if route != "misc_b_then_a" {
					first, second, firstColumn, secondColumn, firstValue, secondValue = "fk_a", "fk_b", "a", "b", 9, 10
				}
				if route == "misc_a_then_b" {
					actions = "VALIDATE CONSTRAINT fk_a,ALTER CONSTRAINT fk_a NOT ENFORCED,VALIDATE CONSTRAINT fk_b,ALTER CONSTRAINT fk_b NOT ENFORCED"
				}
				held := addConstraintHeld(t, p, b, c, "ALTER TABLE t "+actions)
				before := enforcementSnapshot(t, p, b, c)
				validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, addConstraintFK("t", first, firstColumn, fmt.Sprint(firstValue), "p")))
				enforcementUnchanged(t, p, b, c, before)
				run(fixture.Exec("INSERT INTO p VALUES($1)", 1, firstValue))
				before = enforcementSnapshot(t, p, b, c)
				validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, addConstraintFK("t", second, secondColumn, fmt.Sprint(secondValue), "p")))
				enforcementUnchanged(t, p, b, c, before)
				run(fixture.Exec("INSERT INTO p VALUES($1)", 1, secondValue))
				validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
				for _, name := range []string{"fk_a", "fk_b"} {
					oid := enforcementIdentity(t, p, b, c, "t", name, "FOREIGN KEY ("+map[string]string{"fk_a": "a", "fk_b": "b"}[name]+") REFERENCES p(id) NOT ENFORCED", false, false)
					enforcementTriggers(t, p, b, c, oid, false)
				}
			})
		})
	}
}
