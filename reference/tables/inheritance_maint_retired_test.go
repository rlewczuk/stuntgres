package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// PostgreSQL18.6 oracle discovery confirms that historical-only facts retain
// validity, enforcement and deparsed definitions independently of live edges.
func inheritanceMaintRetiredFacts(s *inheritanceCase, table string, count int64) {
	s.run(retiredFlags(table, count))
	s.values("SELECT conname,convalidated,conenforced,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='"+table+"'::regclass::oid ORDER BY conname", []string{"conname", "convalidated", "conenforced", "definition"}, []string{"NAME", "BOOL", "BOOL", "TEXT"}, [][]any{{"ck", true, true, "CHECK ((a > 0))"}, {"nn", true, true, "NOT NULL a"}})
}

func TestReferenceInheritanceMaintRetiredDetach(t *testing.T) {
	// Isolated cases preserve each DROP/TYPE and parent-lifetime witness.
	for _, action := range []string{"Lifetime", "DropCheck", "DropNN", "Type", "ReattachEmpty", "RecreateFact"} {
		t.Run(action, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL,b INT,CONSTRAINT ck CHECK(a>0))", 0)
				s.exec("CREATE TABLE c(a INT) INHERITS(p)", 0)
				s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
				s.exec("INSERT INTO p VALUES(1,10)", 1)
				s.exec("INSERT INTO c VALUES(2,20)", 1)
				s.exec("INSERT INTO leaf VALUES(3,30)", 1)
				nn, ck := inheritanceAlterConstraintOID(s, "c", "nn"), inheritanceAlterConstraintOID(s, "c", "ck")
				s.exec("ALTER TABLE p DROP COLUMN a", 0)
				s.run(retiredFlags("c", 1))
				s.run(retiredFlags("leaf", 1))
				s.exec("ALTER TABLE c NO INHERIT p", 0)
				s.edges("c")
				s.edges("leaf", "c")
				// RemoveInheritance matches live parent facts only; a/nn/ck
				// are gone. Oracle18.6 confirms historical counts remain.
				inheritanceMaintRetiredFacts(s, "c", 1)
				inheritanceMaintRetiredFacts(s, "leaf", 1)
				inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), true, int64(0)}, {"b", int64(2), true, int64(0)}})
				if inheritanceAlterConstraintOID(s, "c", "nn") != nn || inheritanceAlterConstraintOID(s, "c", "ck") != ck {
					s.t.Fatal("last detach replaced retained constraint identity")
				}
				s.values("SELECT b FROM p", []string{"b"}, []string{"INT4"}, [][]any{{int64(10)}})
				s.values("SELECT a,b FROM c ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}})
				for _, table := range []string{"c", "leaf"} {
					s.fail("INSERT INTO "+table+"(a,b) VALUES(NULL,NULL)", inheritanceNullDiagnostic(table, "a", "null, null"))
					s.fail("INSERT INTO "+table+"(a,b) VALUES(0,0)", inheritanceCheckDiagnostic(table, "ck", "0, 0"))
				}
				switch action {
				case "DropCheck", "DropNN":
					name := "ck"
					if action == "DropNN" {
						name = "nn"
					}
					q := "ALTER TABLE c DROP CONSTRAINT " + name
					s.fail(q, fixture.Diagnostic{Code: "42P16", Message: `cannot drop inherited constraint "` + name + `" of relation "c"`})
					inheritanceMaintRetiredFacts(s, "c", 1)
					inheritanceMaintRetiredFacts(s, "leaf", 1)
				case "Type":
					// TYPE removes catalog facts, not the physical NOT NULL flag.
					s.exec("ALTER TABLE c ALTER a TYPE BIGINT USING a+10", 0)
					inheritanceAlterFacts(s, "c", nil)
					inheritanceAlterFacts(s, "leaf", nil)
					inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), true, int64(0)}, {"b", int64(2), true, int64(0)}})
					inheritanceAlterAttributes(s, "leaf", [][]any{{"a", int64(1), false, int64(1)}, {"b", int64(2), false, int64(1)}})
					for _, table := range []string{"c", "leaf"} {
						s.values("SELECT attnotnull FROM pg_attribute WHERE attrelid='"+table+"'::regclass::oid AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}})
					}
					s.values("SELECT a,b FROM c ORDER BY a", []string{"a", "b"}, []string{"INT8", "INT4"}, [][]any{{int64(12), int64(20)}, {int64(13), int64(30)}})
				case "ReattachEmpty":
					s.exec("ALTER TABLE c INHERIT p", 0)
					s.run(retiredFlags("c", 1))
					s.exec("ALTER TABLE c NO INHERIT p", 0)
					s.run(retiredFlags("c", 1))
				case "Lifetime", "RecreateFact":
					s.exec("DROP TABLE p", 0)
					inheritanceMaintRetiredFacts(s, "c", 1)
					inheritanceMaintRetiredFacts(s, "leaf", 1)
					if inheritanceAlterConstraintOID(s, "c", "nn") != nn || inheritanceAlterConstraintOID(s, "c", "ck") != ck {
						s.t.Fatal("former parent deletion changed retained identity")
					}
					for _, table := range []string{"c", "leaf"} {
						s.fail("INSERT INTO "+table+"(a,b) VALUES(NULL,NULL)", inheritanceNullDiagnostic(table, "a", "null, null"))
						s.fail("INSERT INTO "+table+"(a,b) VALUES(0,0)", inheritanceCheckDiagnostic(table, "ck", "0, 0"))
					}
					if action == "Lifetime" {
						s.exec("CREATE TABLE p(b INT)", 0)
					} else {
						s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL,b INT,CONSTRAINT ck CHECK(a>0))", 0)
					}
					s.exec("ALTER TABLE c INHERIT p", 0)
					count := int64(1)
					if action == "RecreateFact" {
						count = 2
					}
					s.run(retiredFlags("c", count))
					s.run(retiredFlags("leaf", 1))
					s.exec("ALTER TABLE c NO INHERIT p", 0)
					s.run(retiredFlags("c", 1))
					s.exec("DROP TABLE p", 0)
					for _, table := range []string{"c", "leaf"} {
						s.fail("INSERT INTO "+table+"(a,b) VALUES(NULL,NULL)", inheritanceNullDiagnostic(table, "a", "null, null"))
						s.fail("INSERT INTO "+table+"(a,b) VALUES(0,0)", inheritanceCheckDiagnostic(table, "ck", "0, 0"))
					}
				}
			})
		})
	}
}
