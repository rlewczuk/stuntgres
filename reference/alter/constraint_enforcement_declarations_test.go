package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConstraintEnforcementDeclarations(t *testing.T) {
	for _, kind := range []string{"check", "fk"} {
		for _, form := range []string{"column_named", "column_unnamed", "table_named", "table_unnamed", "add_named", "add_unnamed"} {
			for _, attribute := range []string{"", " ENFORCED", " NOT ENFORCED"} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/prepared_%v", kind, form, attribute, prepared), func(t *testing.T) {
						addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							run := enforcementRun(t, p, b, c)
							run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
							run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
							name, column, table, def := "ck", "CHECK(a>0)", "CHECK(a>0)", "CHECK ((a > 0))"
							if kind == "fk" {
								name, column, table, def = "fk", "REFERENCES p(id)", "FOREIGN KEY(a) REFERENCES p(id)", "FOREIGN KEY (a) REFERENCES p(id)"
							}
							named := form == "column_named" || form == "table_named" || form == "add_named"
							prefix := ""
							if named {
								prefix = "CONSTRAINT " + name + " "
							} else if kind == "check" {
								name = "t_a_check"
							} else {
								name = "t_a_fkey"
							}
							q := "CREATE TABLE t(a INT,note TEXT," + prefix + table + attribute + ")"
							if form == "column_named" || form == "column_unnamed" {
								q = "CREATE TABLE t(a INT " + prefix + column + attribute + ",note TEXT)"
							}
							if form == "add_named" || form == "add_unnamed" {
								run(fixture.Exec("CREATE TABLE t(a INT,note TEXT)", 0))
								q = "ALTER TABLE t ADD " + prefix + table + attribute
							}
							s := fixture.Exec(q, 0)
							if prepared {
								s = fixture.Prepared(s)
							}
							run(s)
							enforced := attribute != " NOT ENFORCED"
							if !enforced {
								def += " NOT ENFORCED"
							}
							oid := enforcementIdentity(t, p, b, c, "t", name, def, enforced, enforced)
							if kind == "fk" {
								enforcementTriggers(t, p, b, c, oid, enforced)
							}
							run(fixture.Exec("INSERT INTO t VALUES(1,'one'),(NULL,'null')", 2))
							bad := int64(0)
							d := enforcementCheck("t", name, false, "0, bad")
							if kind == "fk" {
								bad = 9
								d = addConstraintFK("t", name, "a", "9", "p")
							}
							for _, q := range []string{"INSERT INTO t VALUES($1,'bad')", "UPDATE t SET a=$1,note='bad' WHERE a=1"} {
								s := fixture.Exec(q, 1, bad)
								if enforced {
									s = fixture.ErrorDiagnostic(q, d, bad)
								}
								if prepared {
									s = fixture.Prepared(s)
								}
								run(s)
							}
							rows := [][]any{{int64(1), "one"}, {nil, "null"}}
							if !enforced {
								rows = [][]any{{bad, "bad"}, {bad, "bad"}, {nil, "null"}}
							}
							q = "SELECT a,note FROM t ORDER BY a NULLS LAST,note"
							run(fixture.Select(q, []string{"a", "note"}, []string{"INT4", "TEXT"}, rows))
							fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, q, []string{"a", "note"}, []string{"INT4", "TEXT"}, []bool{true, true}, len(rows))
						})
					})
				}
			}
		}
	}
}

func TestReferenceConstraintEnforcementFlags(t *testing.T) {
	for _, kind := range []string{"check", "fk"} {
		for _, properties := range []string{"NOT ENFORCED", "NOT ENFORCED NOT ENFORCED", "NOT VALID NOT ENFORCED", "NOT ENFORCED NOT VALID", "ENFORCED NOT VALID", "NOT VALID ENFORCED", "ENFORCED ENFORCED"} {
			t.Run(kind+"/"+properties, func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := addConstraintSetup(t, p, b, c)
					clause, name := "CHECK(a>0)", "ck"
					if kind == "fk" {
						clause, name = "FOREIGN KEY(a) REFERENCES p(id)", "fk"
					}
					hasBadHistory := properties == "ENFORCED NOT VALID" || properties == "NOT VALID ENFORCED"
					if hasBadHistory {
						bad := 0
						if kind == "fk" {
							bad = 9
						}
						run(fixture.Exec("INSERT INTO t VALUES($1,'history')", 1, bad))
					}
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT "+name+" "+clause+" "+properties, 0))
					falseState := properties == "NOT ENFORCED" || properties == "NOT ENFORCED NOT ENFORCED" || properties == "NOT VALID NOT ENFORCED" || properties == "NOT ENFORCED NOT VALID"
					valid := properties == "ENFORCED ENFORCED"
					run(enforcementFlags("t", name, !falseState, valid))
					if falseState {
						run(fixture.Exec("INSERT INTO t VALUES(0,'false')", 1))
						for _, prepared := range []bool{false, true} {
							s := fixture.ErrorDiagnostic("ALTER TABLE t VALIDATE CONSTRAINT "+name, fixture.ControlsReferenceDiagnostic("55000", "cannot validate NOT ENFORCED constraint"))
							if prepared {
								s = fixture.Prepared(s)
							}
							run(s)
						}
						run(enforcementFlags("t", name, false, false))
					} else {
						d := enforcementCheck("t", name, false, "0, bad")
						bad := 0
						if kind == "fk" {
							bad = 9
							d = addConstraintFK("t", name, "a", "9", "p")
						}
						run(fixture.ErrorDiagnostic("INSERT INTO t VALUES($1,'bad')", d, bad))
						if kind == "fk" {
							run(fixture.Prepared(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT fk ENFORCED", 0)))
							run(enforcementFlags("t", "fk", true, valid))
						}
						if hasBadHistory {
							history := enforcementCheck("t", name, true, "")
							if kind == "fk" {
								history = addConstraintFK("t", name, "a", "9", "p")
							}
							before := enforcementSnapshot(t, p, b, c)
							run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE t VALIDATE CONSTRAINT "+name, history)))
							enforcementUnchanged(t, p, b, c, before)
							run(fixture.Exec("UPDATE t SET a=1", 1))
						}
						run(fixture.Exec("ALTER TABLE t VALIDATE CONSTRAINT "+name, 0))
						run(enforcementFlags("t", name, true, true))
					}
				})
			})
		}
	}
}

// These properties retain the existing nondeferrable/immediate state. Released
// PG18's repeated ConstraintAttributeSpec admits either order with enforcement.
func TestReferenceConstraintEnforcementPropertyOrder(t *testing.T) {
	for _, form := range []string{"column", "table", "add"} {
		for _, attribute := range []string{"ENFORCED", "NOT ENFORCED"} {
			for _, property := range []string{"NOT DEFERRABLE", "INITIALLY IMMEDIATE", "NOT DEFERRABLE INITIALLY IMMEDIATE"} {
				for _, enforcementFirst := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/%s/%s/enforcement_first_%v/prepared_%v", form, attribute, property, enforcementFirst, prepared), func(t *testing.T) {
							addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
								run := enforcementRun(t, p, b, c)
								run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
								run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
								properties := property + " " + attribute
								if enforcementFirst {
									properties = attribute + " " + property
								}
								q := "CREATE TABLE t(a INT,CONSTRAINT fk FOREIGN KEY(a) REFERENCES p " + properties + ")"
								if form == "column" {
									q = "CREATE TABLE t(a INT CONSTRAINT fk REFERENCES p " + properties + ")"
								}
								if form == "add" {
									run(fixture.Exec("CREATE TABLE t(a INT)", 0))
									q = "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p " + properties
								}
								step := fixture.Exec(q, 0)
								if prepared {
									step = fixture.Prepared(step)
								}
								run(step)
								enforced := attribute == "ENFORCED"
								definition := "FOREIGN KEY (a) REFERENCES p(id)"
								if !enforced {
									definition += " NOT ENFORCED"
								}
								oid := enforcementIdentity(t, p, b, c, "t", "fk", definition, enforced, enforced)
								enforcementTriggers(t, p, b, c, oid, enforced)
								run(fixture.Select("SELECT condeferrable,condeferred FROM pg_constraint WHERE conrelid='t'::regclass AND conname='fk'", []string{"condeferrable", "condeferred"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}}))
								run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
								step = fixture.Exec("INSERT INTO t VALUES(9)", 1)
								if enforced {
									step = fixture.ErrorDiagnostic("INSERT INTO t VALUES(9)", addConstraintFK("t", "fk", "a", "9", "p"))
								}
								run(step)
							})
						})
					}
				}
			}
		}
	}
}
