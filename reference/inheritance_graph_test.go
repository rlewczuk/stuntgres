package reference_test

import "testing"

func TestReferenceInheritanceGraphLifecycle(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE base(a INT DEFAULT 7 NOT NULL,CONSTRAINT positive CHECK(a>0))", 0)
		s.exec("CREATE TABLE left_parent(l TEXT) INHERITS(base)", 0)
		s.exec("CREATE TABLE right_parent(r BOOL) INHERITS(base)", 0)
		s.exec("CREATE TABLE leaf(z BIGINT) INHERITS(left_parent,right_parent)", 0)
		s.exec("CREATE TABLE direct_leaf() INHERITS(base,left_parent)", 0)
		s.exec("INSERT INTO leaf(l,r,z) VALUES('leaf',true,11)", 1)
		s.values("SELECT * FROM leaf", []string{"a", "l", "r", "z"}, []string{"INT4", "TEXT", "BOOL", "INT8"}, [][]any{{int64(7), "leaf", true, int64(11)}})
		s.edges("left_parent", "base")
		s.edges("right_parent", "base")
		s.edges("leaf", "left_parent", "right_parent")
		s.edges("direct_leaf", "base", "left_parent")
		if s.b.pg {
			s.values("SELECT conname,coninhcount,conislocal FROM pg_constraint WHERE conrelid='leaf'::regclass ORDER BY conname", []string{"conname", "coninhcount", "conislocal"}, []string{"NAME", "INT2", "BOOL"}, [][]any{{"base_a_not_null", int64(2), false}, {"positive", int64(2), false}})
			s.values("SELECT inhseqno,inhparent::regclass::text AS parent FROM pg_inherits WHERE inhrelid='leaf'::regclass ORDER BY inhseqno", []string{"inhseqno", "parent"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "left_parent"}, {int64(2), "right_parent"}})
		}
		s.exec("DROP TABLE direct_leaf", 0)
		s.exec("DROP TABLE leaf", 0)
		s.values("SELECT relname FROM pg_class WHERE relname='base' OR relname='left_parent' OR relname='right_parent' ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{{"base"}, {"left_parent"}, {"right_parent"}})
		s.exec("CREATE TABLE leaf(id SERIAL,UNIQUE(id)) INHERITS(left_parent,right_parent)", 0)
		s.exec("CREATE TABLE unrelated() INHERITS(right_parent)", 0)
		s.exec("CREATE VIEW leaf_view AS SELECT a FROM leaf", 0)
		s.exec("CREATE INDEX leaf_a_idx ON leaf(a)", 0)
		s.exec("INSERT INTO leaf DEFAULT VALUES", 1)
		s.exec("DROP TABLE left_parent CASCADE", 0)
		for _, name := range []string{"left_parent", "leaf", "leaf_view", "leaf_id_seq", "leaf_a_idx"} {
			s.values("SELECT relname FROM pg_class WHERE relname='"+name+"'", []string{"relname"}, []string{"NAME"}, nil)
		}
		s.exec("INSERT INTO unrelated DEFAULT VALUES", 1)
		s.values("SELECT a FROM unrelated", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
		s.edges("unrelated", "right_parent")
		s.exec("DROP TABLE base,right_parent,unrelated RESTRICT", 0)
	})
}

func TestReferenceInheritanceDropRestrict(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(2)", 1)
		s.fail("DROP TABLE p RESTRICT", inheritanceDropDiagnostic("p", "c"))
		s.values("SELECT * FROM c", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}})
		s.edges("c", "p")
		if s.b.pg {
			s.values("SELECT * FROM ONLY p", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
		}
		s.exec("DROP TABLE p,c RESTRICT", 0)
		s.absent("p")
		s.absent("c")
	})
}

func TestReferenceInheritanceSchemaLifecycle(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE SCHEMA inheritance_schema", 0)
		defer func() {
			if _, err := s.c.ExecContext(s.p.ctx, "DROP SCHEMA IF EXISTS inheritance_schema CASCADE"); err != nil {
				s.t.Error(err)
			}
		}()
		s.exec("CREATE TABLE inheritance_schema.p(a INT)", 0)
		s.exec("CREATE TABLE c() INHERITS(inheritance_schema.p)", 0)
		s.exec("CREATE VIEW v AS SELECT a FROM c", 0)
		s.exec("DROP SCHEMA inheritance_schema CASCADE", 0)
		s.absent("c")
		s.absent("v")
		s.exec("CREATE TEMP TABLE tp(a INT)", 0)
		s.exec("CREATE TEMP TABLE tc() INHERITS(tp)", 0)
		s.exec("INSERT INTO tc VALUES(8)", 1)
		s.fail("DROP TABLE tp RESTRICT", inheritanceDropDiagnostic("tp", "tc"))
		s.exec("DROP TABLE tp CASCADE", 0)
		s.absent("tc")
		// ON COMMIT cleanup must use the same complete graph closure.
		s.exec("BEGIN", 0)
		s.exec("CREATE TEMP TABLE commit_parent(a INT) ON COMMIT DROP", 0)
		s.exec("CREATE TEMP TABLE commit_child() INHERITS(commit_parent)", 0)
		s.exec("COMMIT", 0)
		s.absent("commit_parent")
		s.absent("commit_child")
	})
}
