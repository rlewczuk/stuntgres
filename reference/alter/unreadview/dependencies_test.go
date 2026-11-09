package unreadview_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

const dropHint = "Use DROP ... CASCADE to drop the dependent objects too."

func TestReferenceAlterTypeUnreadViewDependencies(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"projection", "SELECT id,state FROM t"},
		{"where", "SELECT id FROM t WHERE state='active'"},
		{"order", "SELECT id FROM t ORDER BY state"},
		{"join", "SELECT t.id FROM t JOIN other ON t.state=other.state"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routes(t, func(s *scenario, mode string) {
				s.exec("CREATE TYPE oldenum AS ENUM ('active')", 0)
				s.exec("CREATE TABLE t(id INT,state oldenum)", 0)
				s.exec("INSERT INTO t VALUES(1,'active'),(2,NULL)", 2)
				s.exec("CREATE TABLE other(state oldenum)", 0)
				s.exec("INSERT INTO other VALUES('active')", 1)
				s.exec("CREATE VIEW v AS "+tc.query, 0)
				old, identity := s.oid("oldenum"), s.identity()
				definition := s.viewDefinition("v")
				s.exec("CREATE TYPE replacement AS ENUM ('active','dormant')", 0)
				aborted := s.prepare(s.conn, "SELECT id FROM t")
				finish := s.begin(mode)
				s.exec("SAVEPOINT dependency", 0)
				s.failure("ALTER TABLE t ALTER state TYPE replacement USING state::text::replacement", "0A000", "cannot alter type of a column used by a view or rule", `rule _RETURN on view v depends on column "state"`, "")
				s.aborted(aborted, "SELECT id FROM t")
				s.exec("ROLLBACK TO dependency", 0)
				s.exec("RELEASE dependency", 0)
				finish(true)
				if s.oid("oldenum") != old {
					s.t.Fatal("read rejection changed enum identity")
				}
				s.sameIdentity(identity)
				if s.viewDefinition("v") != definition {
					s.t.Fatal("read rejection changed view definition")
				}
				if s.scalar("SELECT atttypid::BIGINT FROM pg_attribute WHERE attrelid='t'::regclass AND attname='state'") != old {
					s.t.Fatal("read rejection changed source usage")
				}
				s.links()
				s.run(fixture.Select("SELECT id,state::text FROM t ORDER BY id", []string{"id", "state"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "active"}, {int64(2), nil}}))
				if tc.name == "projection" {
					s.run(fixture.Select("SELECT id,state::text FROM v ORDER BY id", []string{"id", "state"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "active"}, {int64(2), nil}}))
					if s.scalar("SELECT atttypid::BIGINT FROM pg_attribute WHERE attrelid='v'::regclass AND attname='state'") != old {
						s.t.Fatal("projection output type changed")
					}
				} else if tc.name == "order" {
					s.run(ids("v", 1, 2))
				} else {
					s.run(ids("v", 1))
				}
			})
		})
	}
	t.Run("table_column_and_original_type_edges", func(t *testing.T) {
		routes(t, func(s *scenario, mode string) {
			s.setup(true, false)
			identity, old := s.identity(), s.oid("oldenum")
			s.links()
			s.failure("DROP TYPE oldenum", "2BP01", "cannot drop type oldenum because other objects depend on it", "column state of table t depends on type oldenum", dropHint)
			s.failure("DROP TABLE t RESTRICT", "2BP01", "cannot drop table t because other objects depend on it", "view v depends on table t", dropHint)
			s.failure("ALTER TABLE t DROP COLUMN id RESTRICT", "2BP01", "cannot drop column id of table t because other objects depend on it", "view v depends on column id of table t", dropHint)
			s.sameIdentity(identity)
			if s.oid("oldenum") != old {
				s.t.Fatal("DROP rejection changed type")
			}
			s.run(ids("v", 1, 2))
			finish := s.begin(mode)
			s.replacement()
			finish(true)
			s.run(ids("v", 1, 2))
			s.links()
		})
	})
	t.Run("independent_holder", func(t *testing.T) {
		routes(t, func(s *scenario, mode string) {
			s.setup(true, true)
			s.exec("CREATE TABLE holder(value oldenum)", 0)
			s.exec("INSERT INTO holder VALUES('active')", 1)
			finish := s.begin(mode)
			s.exec("CREATE TYPE replacement AS ENUM ('active','dormant')", 0)
			s.exec("ALTER TABLE t ALTER state TYPE replacement USING state::text::replacement", 0)
			if s.scalar("SELECT count(*) FROM pg_depend d JOIN pg_attribute a ON a.attrelid=d.objid AND a.attnum=d.objsubid WHERE d.classid='pg_class'::regclass AND d.refclassid='pg_type'::regclass AND d.objid='holder'::regclass AND a.attname='value' AND d.refobjid='oldenum'::regtype") < 1 {
				s.t.Fatal("missing independent holder type edge")
			}
			s.exec("SAVEPOINT holder_edge", 0)
			s.failure("DROP TYPE oldenum", "2BP01", "cannot drop type oldenum because other objects depend on it", "column value of table holder depends on type oldenum", dropHint)
			s.exec("ROLLBACK TO holder_edge", 0)
			s.run(fixture.Select("SELECT value::text FROM holder", []string{"value"}, []string{"TEXT"}, [][]any{{"active"}}))
			s.exec("DROP TABLE holder", 0)
			s.exec("DROP TYPE oldenum", 0)
			s.exec("ALTER TYPE replacement RENAME TO oldenum", 0)
			finish(true)
			s.graph(1, 2)
			s.links()
		})
	})
	t.Run("explicit_enum_expression", func(t *testing.T) {
		routes(t, func(s *scenario, mode string) {
			s.setup(true, true)
			s.exec("CREATE VIEW typed_v AS SELECT ('active'::oldenum)::text AS label FROM t", 0)
			if s.scalar("SELECT count(*) FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid WHERE d.classid='pg_rewrite'::regclass AND d.refclassid='pg_type'::regclass AND r.ev_class='typed_v'::regclass AND d.refobjid='oldenum'::regtype") < 1 {
				s.t.Fatal("missing explicit type dependency")
			}
			finish := s.begin(mode)
			s.exec("CREATE TYPE replacement AS ENUM ('active','dormant')", 0)
			s.exec("ALTER TABLE t ALTER state TYPE replacement USING state::text::replacement", 0)
			s.exec("SAVEPOINT expression_edge", 0)
			s.failure("DROP TYPE oldenum", "2BP01", "cannot drop type oldenum because other objects depend on it", "view typed_v depends on type oldenum", dropHint)
			s.exec("ROLLBACK TO expression_edge", 0)
			step := fixture.Select("SELECT label FROM typed_v", []string{"label"}, []string{"TEXT"}, [][]any{{"active"}, {"active"}})
			step.Bag = true
			s.run(step)
			s.exec("DROP VIEW typed_v", 0)
			s.exec("DROP TYPE oldenum", 0)
			s.exec("ALTER TYPE replacement RENAME TO oldenum", 0)
			finish(true)
			s.graph(1, 2)
			s.links()
		})
	})
	t.Run("add_value_same_identity", func(t *testing.T) {
		routes(t, func(s *scenario, mode string) {
			s.setup(true, true)
			old, identity := s.oid("oldenum"), s.identity()
			held := s.prepare(s.conn, ids("v").Query)
			finish := s.begin(mode)
			s.exec("ALTER TYPE oldenum ADD VALUE 'dormant'", 0)
			finish(true)
			if s.oid("oldenum") != old {
				s.t.Fatal("ADD VALUE replaced type identity")
			}
			s.sameIdentity(identity)
			s.graph(1, 2)
			s.exec("INSERT INTO t VALUES(3,'dormant')", 1)
			s.exec("INSERT INTO marker VALUES(3)", 1)
			s.graph(1, 2, 3)
			s.heldRead(held, ids("v", 1, 2, 3))
			s.links()
		})
	})
}
