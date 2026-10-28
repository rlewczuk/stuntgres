package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func enforcementOrigin(table string, local bool, count int64, enforced, valid bool) fixture.Step {
	return fixture.Select("SELECT conislocal,coninhcount,conenforced,convalidated FROM pg_constraint WHERE conrelid=$1::regclass AND conname='ck'", []string{"conislocal", "coninhcount", "conenforced", "convalidated"}, []string{"BOOL", "INT2", "BOOL", "BOOL"}, [][]any{{local, count, enforced, valid}}, table)
}

func TestReferenceConstraintEnforcementInheritance(t *testing.T) {
	enforcementInheritanceEdges(t)
	for _, form := range []string{"create", "add", "inherit"} {
		t.Run("false_propagation/"+form, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := enforcementRun(t, p, b, c)
				if form == "add" {
					run(fixture.Exec("CREATE TABLE t(a INT)", 0))
					run(fixture.Exec("CREATE TABLE child() INHERITS(t)", 0))
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT ENFORCED", 0))
				} else {
					run(fixture.Exec("CREATE TABLE t(a INT,CONSTRAINT ck CHECK(a>0) NOT ENFORCED)", 0))
					if form == "create" {
						run(fixture.Exec("CREATE TABLE child() INHERITS(t)", 0))
					} else {
						run(fixture.Exec("CREATE TABLE child(a INT,CONSTRAINT ck CHECK(a>0) NOT ENFORCED)", 0))
						run(fixture.Prepared(fixture.Exec("ALTER TABLE child INHERIT t", 0)))
					}
				}
				run(fixture.Exec("INSERT INTO child VALUES(0)", 1))
				run(enforcementOrigin("t", true, 0, false, false))
				run(enforcementOrigin("child", form == "inherit", 1, false, false))
				before := enforcementSnapshot(t, p, b, c)
				run(fixture.ErrorDiagnostic("ALTER TABLE t VALIDATE CONSTRAINT ck", fixture.ControlsReferenceDiagnostic("55000", "cannot validate NOT ENFORCED constraint")))
				enforcementUnchanged(t, p, b, c, before)
				if form == "inherit" {
					run(fixture.Exec("ALTER TABLE child NO INHERIT t", 0))
					run(enforcementOrigin("child", true, 0, false, false))
				}
				run(fixture.Select("SELECT a FROM child", []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}}))
			})
		})
	}
	for _, childFirst := range []bool{false, true} {
		for _, invalid := range []bool{false, true} {
			t.Run(fmt.Sprintf("true_child_false_parent/child_first_%v/invalid_%v", childFirst, invalid), func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := enforcementRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE t(a INT)", 0))
					run(fixture.Exec("CREATE TABLE child() INHERITS(t)", 0))
					child := "ALTER TABLE child ADD CONSTRAINT ck CHECK(a>0) ENFORCED"
					if invalid {
						child += " NOT VALID"
					}
					parent := "ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT ENFORCED"
					if childFirst {
						run(fixture.Exec(child, 0))
						run(fixture.Exec(parent, 0))
					} else {
						run(fixture.Exec(parent, 0))
						run(fixture.Exec(child, 0))
					}
					run(enforcementOrigin("t", true, 0, false, false))
					// Released inherit.out: existing invalid child retains invalid;
					// adding enforced-invalid to an inherited false definition validates it.
					run(enforcementOrigin("child", true, 1, true, !invalid || !childFirst))
					run(fixture.Exec("INSERT INTO t VALUES(0)", 1))
					run(fixture.ErrorDiagnostic("INSERT INTO child VALUES(0)", enforcementCheck("child", "ck", false, "0")))
				})
			})
		}
	}
	for _, childFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("false_child_true_parent/child_first_%v", childFirst), func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := enforcementRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE t(a INT)", 0))
				run(fixture.Exec("CREATE TABLE child() INHERITS(t)", 0))
				first, second := "ALTER TABLE child ADD CONSTRAINT ck CHECK(a>0) NOT ENFORCED", "ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) ENFORCED"
				if !childFirst {
					first, second = second, first
				}
				run(fixture.Exec(first, 0))
				before := enforcementSnapshot(t, p, b, c)
				run(fixture.Prepared(fixture.ErrorDiagnostic(second, fixture.ControlsReferenceDiagnostic("42P17", `constraint "ck" conflicts with NOT ENFORCED constraint on relation "child"`))))
				enforcementUnchanged(t, p, b, c, before)
			})
		})
	}
	t.Run("multiple_parents", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			for _, q := range []string{"CREATE TABLE t(a INT,CONSTRAINT ck CHECK(a>0) NOT ENFORCED)", "CREATE TABLE other(a INT,CONSTRAINT ck CHECK(a>0) ENFORCED)", "CREATE TABLE child() INHERITS(t,other)"} {
				run(fixture.Exec(q, 0))
			}
			run(enforcementOrigin("child", false, 2, true, true))
			run(fixture.ErrorDiagnostic("INSERT INTO child VALUES(0)", enforcementCheck("child", "ck", false, "0")))
			run(fixture.Exec("INSERT INTO t VALUES(0)", 1))
		})
	})
	t.Run("no_inherit_and_local_fk", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE t(a INT,CONSTRAINT ck CHECK(a>0) NO INHERIT NOT ENFORCED,CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED)", "CREATE TABLE child() INHERITS(t)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='child'::regclass", []string{"conname"}, []string{"NAME"}, nil))
			run(fixture.Exec("INSERT INTO child VALUES(0)", 1))
			run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT fk ENFORCED", 0))
			run(fixture.Exec("INSERT INTO child VALUES(9)", 1))
		})
	})
	t.Run("late_descendant_replacement_atomic", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			for _, q := range []string{"CREATE TABLE t(a INT,CONSTRAINT ck CHECK(a>0) NOT ENFORCED)", "CREATE TABLE early() INHERITS(t)", "CREATE TABLE late() INHERITS(t)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO early VALUES(1)", 1))
			run(fixture.Exec("INSERT INTO late VALUES(0)", 1))
			before := enforcementSnapshot(t, p, b, c)
			held := addConstraintHeld(t, p, b, c, "ALTER TABLE t DROP CONSTRAINT ck, ADD CONSTRAINT ck CHECK(a>0) ENFORCED")
			validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, enforcementCheck("late", "ck", true, "")))
			enforcementUnchanged(t, p, b, c, before)
			run(fixture.Exec("UPDATE late SET a=2", 1))
			validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
			for _, table := range []string{"t", "early", "late"} {
				run(enforcementFlags(table, "ck", true, true))
			}
		})
	})
}

func TestReferenceConstraintEnforcementPartitions(t *testing.T) {
	enforcementPartitionEdges(t)
	for _, form := range []string{"create", "add"} {
		t.Run("propagation/"+form, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := enforcementRun(t, p, b, c)
				q := "CREATE TABLE t(k INT,a INT) PARTITION BY RANGE(k)"
				if form == "create" {
					q = "CREATE TABLE t(k INT,a INT,CONSTRAINT ck CHECK(a>0) NOT ENFORCED) PARTITION BY RANGE(k)"
				}
				run(fixture.Exec(q, 0))
				for _, q := range []string{"CREATE TABLE mid PARTITION OF t DEFAULT PARTITION BY RANGE(k)", "CREATE TABLE early PARTITION OF mid FOR VALUES FROM(0) TO(10)", "CREATE TABLE late PARTITION OF mid DEFAULT"} {
					run(fixture.Exec(q, 0))
				}
				if form == "add" {
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT ENFORCED", 0))
				}
				run(fixture.Exec("INSERT INTO t VALUES(1,1),(20,0)", 2))
				for _, table := range []string{"t", "mid", "early", "late"} {
					run(enforcementOrigin(table, table == "t", map[bool]int64{true: 0, false: 1}[table == "t"], false, false))
				}
				before := enforcementSnapshot(t, p, b, c)
				held := addConstraintHeld(t, p, b, c, "ALTER TABLE t DROP CONSTRAINT ck, ADD CONSTRAINT ck CHECK(a>0) ENFORCED")
				validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, enforcementCheck("late", "ck", true, "")))
				enforcementUnchanged(t, p, b, c, before)
				run(fixture.Exec("UPDATE t SET a=2 WHERE k=20", 1))
				validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
				for _, table := range []string{"t", "mid", "early", "late"} {
					run(enforcementFlags(table, "ck", true, true))
				}
				run(fixture.Select("SELECT k,a FROM t ORDER BY k", []string{"k", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(20), int64(2)}}))
			})
		})
	}
	for _, childEnforced := range []bool{false, true} {
		t.Run(fmt.Sprintf("attach/child_enforced_%v", childEnforced), func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := enforcementRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE t(k INT,a INT,CONSTRAINT ck CHECK(a>0) NOT ENFORCED) PARTITION BY RANGE(k)", 0))
				attribute, value := "NOT ENFORCED", 0
				if childEnforced {
					attribute, value = "ENFORCED", 1
				}
				run(fixture.Exec("CREATE TABLE leaf(k INT,a INT,CONSTRAINT ck CHECK(a>0) "+attribute+")", 0))
				run(fixture.Exec("INSERT INTO leaf VALUES(1,$1)", 1, value))
				run(fixture.Prepared(fixture.Exec("ALTER TABLE t ATTACH PARTITION leaf FOR VALUES FROM(0) TO(10)", 0)))
				run(enforcementOrigin("leaf", false, 1, childEnforced, childEnforced))
				run(fixture.Select("SELECT k,a FROM t", []string{"k", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(value)}}))
				before := enforcementSnapshot(t, p, b, c)
				d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "leaf" violates partition constraint`)
				d.Detail, d.Schema, d.Table = fmt.Sprintf("Failing row contains (20, %d).", value), "public", "leaf"
				run(fixture.ErrorDiagnostic("INSERT INTO leaf VALUES(20,$1)", d, value))
				enforcementUnchanged(t, p, b, c, before)
			})
		})
	}
	t.Run("attach_bound_scan_remains_required", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(k INT,a INT,CONSTRAINT ck CHECK(a>0) NOT ENFORCED) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE leaf(k INT,a INT,CONSTRAINT ck CHECK(a>0) NOT ENFORCED)", 0))
			run(fixture.Exec("INSERT INTO leaf VALUES(20,0)", 1))
			before := enforcementSnapshot(t, p, b, c)
			d := fixture.ControlsReferenceDiagnostic("23514", `partition constraint of relation "leaf" is violated by some row`)
			d.Schema, d.Table = "public", "leaf"
			run(fixture.ErrorDiagnostic("ALTER TABLE t ATTACH PARTITION leaf FOR VALUES FROM(0) TO(10)", d))
			enforcementUnchanged(t, p, b, c, before)
			run(fixture.Select("SELECT k,a FROM leaf", []string{"k", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(20), int64(0)}}))
		})
	})
}
