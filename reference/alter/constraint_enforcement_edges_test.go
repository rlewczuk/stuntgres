package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func enforcementActionEdges(t *testing.T) {
	t.Run("check_multirow_atomic", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(a INT,CONSTRAINT ck CHECK(a>0) NOT ENFORCED)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(0),(-1)", 2))
			run(fixture.Exec("DELETE FROM t", 2))
			run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT ck, ADD CONSTRAINT ck CHECK(a>0) ENFORCED", 0))
			before := enforcementSnapshot(t, p, b, c)
			run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1),(0)", enforcementCheck("t", "ck", false, "0"))))
			enforcementUnchanged(t, p, b, c, before)
			run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
			before = enforcementSnapshot(t, p, b, c)
			run(fixture.ErrorDiagnostic("UPDATE t SET a=0", enforcementCheck("t", "ck", false, "0")))
			enforcementUnchanged(t, p, b, c, before)
		})
	})
	t.Run("replication_role_does_not_create_absent_triggers", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED", 0))
			for _, role := range []string{"replica", "origin"} {
				run(fixture.Exec("SET session_replication_role="+role, 0))
				run(fixture.Exec("INSERT INTO t VALUES(9,'absent')", 1))
			}
			run(fixture.Exec("DELETE FROM t", 2))
			run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT fk ENFORCED", 0))
			run(fixture.Exec("SET session_replication_role=replica", 0))
			run(fixture.Exec("INSERT INTO t VALUES(9,'replica')", 1))
			run(fixture.Exec("SET session_replication_role=origin", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(10,'origin')", addConstraintFK("t", "fk", "a", "10", "p")))
			run(fixture.Exec("DELETE FROM t", 1))
		})
	})
}

func enforcementInheritanceEdges(t *testing.T) {
	for _, childEnforced := range []bool{false, true} {
		t.Run(fmt.Sprintf("inherit_merge/child_enforced_%v", childEnforced), func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := enforcementRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE t(a INT,CONSTRAINT ck CHECK(a>0) ENFORCED)", 0))
				attribute := "NOT ENFORCED"
				if childEnforced {
					attribute = "ENFORCED"
				}
				run(fixture.Exec("CREATE TABLE child(a INT,CONSTRAINT ck CHECK(a>0) "+attribute+")", 0))
				before := enforcementSnapshot(t, p, b, c)
				q := "ALTER TABLE child INHERIT t"
				if childEnforced {
					run(fixture.Prepared(fixture.Exec(q, 0)))
					run(enforcementOrigin("child", true, 1, true, true))
				} else {
					run(fixture.Prepared(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42P17", `constraint "ck" conflicts with NOT ENFORCED constraint on child table "child"`))))
					enforcementUnchanged(t, p, b, c, before)
				}
			})
		})
	}
	for _, kind := range []string{"create", "add"} {
		t.Run("no_inherit/"+kind, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := enforcementRun(t, p, b, c)
				if kind == "create" {
					run(fixture.Exec("CREATE TABLE t(a INT,CONSTRAINT ck CHECK(a>0) NO INHERIT NOT ENFORCED)", 0))
				} else {
					run(fixture.Exec("CREATE TABLE t(a INT)", 0))
				}
				run(fixture.Exec("CREATE TABLE child() INHERITS(t)", 0))
				if kind == "add" {
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NO INHERIT NOT ENFORCED", 0))
				}
				run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='child'::regclass", []string{"conname"}, []string{"NAME"}, nil))
				run(fixture.Exec("INSERT INTO child VALUES(0)", 1))
				run(fixture.Exec("INSERT INTO t VALUES(0)", 1))
			})
		})
	}
}

func enforcementPartitionEdges(t *testing.T) {
	t.Run("true_parent_rejects_false_leaf", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(k INT,a INT,CONSTRAINT ck CHECK(a>0) ENFORCED) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE leaf(k INT,a INT,CONSTRAINT ck CHECK(a>0) NOT ENFORCED)", 0))
			before := enforcementSnapshot(t, p, b, c)
			run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE t ATTACH PARTITION leaf FOR VALUES FROM(0) TO(10)", fixture.ControlsReferenceDiagnostic("42P17", `constraint "ck" conflicts with NOT ENFORCED constraint on child table "leaf"`))))
			enforcementUnchanged(t, p, b, c, before)
		})
	})
}
