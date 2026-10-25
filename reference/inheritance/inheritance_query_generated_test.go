package reference_test

import "testing"

func TestReferenceInheritanceQueryGenerated(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE q(y BOOL,a INT)", 0)
		for _, kind := range []string{"STORED", "VIRTUAL"} {
			parent, child := "stored_parent", "stored_child"
			if kind == "VIRTUAL" {
				parent, child = "virtual_parent", "virtual_child"
			}
			s.exec("CREATE TABLE "+parent+"(a INT,g INT GENERATED ALWAYS AS(a+1) "+kind+")", 0)
			s.exec("CREATE TABLE "+child+"(g INT GENERATED ALWAYS AS(a+100) "+kind+") INHERITS(q,"+parent+")", 0)
			s.exec("INSERT INTO "+child+"(y,a) VALUES(true,2),(false,NULL)", 2)
			value := int64(102)
			if kind == "VIRTUAL" {
				value = 3
			}
			s.values("SELECT a,g FROM "+parent+" ORDER BY a", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(2), value}, {nil, nil}})
			s.values("SELECT g FROM "+child+" WHERE a=2", []string{"g"}, []string{"INT4"}, [][]any{{int64(102)}})
		}
		s.values("SELECT v.g FROM (VALUES(2),(9)) k(a) LEFT JOIN virtual_parent v ON k.a=v.a ORDER BY k.a", []string{"g"}, []string{"INT4"}, [][]any{{int64(3)}, {nil}})
		s.values("SELECT count(*) AS n FROM virtual_parent WHERE a IS NULL", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
		// Generated and ordinary reads both retain the actual physical leaf.
		s.exec("CREATE TABLE oid_parent(a INT,physical OID GENERATED ALWAYS AS(tableoid) VIRTUAL)", 0)
		s.exec("CREATE TABLE oid_child() INHERITS(q,oid_parent)", 0)
		s.exec("INSERT INTO oid_parent(a) VALUES(1)", 1)
		s.exec("INSERT INTO oid_child(y,a) VALUES(true,2)", 1)
		s.values("SELECT a,physical='oid_parent'::regclass::oid AS is_parent,physical='oid_child'::regclass::oid AS is_child FROM oid_parent ORDER BY a", []string{"a", "is_parent", "is_child"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, false}, {int64(2), false, true}})
		s.values("SELECT tableoid='oid_child'::regclass AS is_child FROM oid_parent WHERE a=2", []string{"is_child"}, []string{"BOOL"}, [][]any{{true}})
	})
}
