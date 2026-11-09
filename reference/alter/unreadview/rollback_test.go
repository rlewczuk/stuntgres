package unreadview_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterTypeUnreadViewRollback(t *testing.T) {
	t.Run("outer_preexisting", func(t *testing.T) {
		routes(t, func(s *scenario, mode string) {
			s.setup(true, true)
			old, identity, definitions := s.oid("oldenum"), s.identity(), s.definitions()
			nested := s.nestedIdentity()
			held := s.prepare(s.conn, ids("v").Query)
			finish := s.begin(mode)
			s.replacement()
			s.exec("INSERT INTO t VALUES(3,'dormant')", 1)
			s.run(ids("v", 1, 2, 3))
			finish(false)
			s.unchanged(old, identity, definitions)
			if got := s.nestedIdentity(); !reflect.DeepEqual(got, nested) {
				s.t.Fatal("rollback changed nested identities", got, nested)
			}
			s.heldRead(held, ids("v", 1, 2))
			invalidDormant(s)
		})
	})
	t.Run("outer_same_transaction", func(t *testing.T) {
		routes(t, func(s *scenario, mode string) {
			finish := s.begin(mode)
			s.setup(true, true)
			s.replacement()
			s.graph(1, 2)
			finish(false)
			s.run(fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('t','v','w','j','marker')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			s.run(fixture.Select("SELECT count(*) FROM pg_type WHERE typname IN ('oldenum','replacement')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			s.exec("CREATE TYPE oldenum AS ENUM ('active')", 0)
			s.exec("CREATE TABLE t(id INT,state oldenum)", 0)
			s.exec("INSERT INTO t VALUES(1,'active')", 1)
		})
	})
	t.Run("older_and_newer_savepoints", func(t *testing.T) {
		routes(t, func(s *scenario, mode string) {
			s.setup(true, true)
			old, identity, definitions := s.oid("oldenum"), s.identity(), s.definitions()
			nested := s.nestedIdentity()
			held := s.prepare(s.conn, ids("v").Query)
			finish := s.begin(mode)
			s.exec("SAVEPOINT original", 0)
			s.replacement()
			replacement := s.oid("oldenum")
			if replacement == old {
				s.t.Fatal("replacement identity unchanged")
			}
			s.exec("SAVEPOINT rewritten", 0)
			s.exec("INSERT INTO t VALUES(3,'dormant')", 1)
			s.exec("INSERT INTO marker VALUES(3)", 1)
			s.graph(1, 2, 3)
			s.exec("ROLLBACK TO rewritten", 0)
			if s.oid("oldenum") != replacement {
				s.t.Fatal("newer mark lost replacement")
			}
			s.graph(1, 2)
			s.links()
			s.exec("RELEASE rewritten", 0)
			s.exec("ROLLBACK TO original", 0)
			s.unchanged(old, identity, definitions)
			if got := s.nestedIdentity(); !reflect.DeepEqual(got, nested) {
				s.t.Fatal("older mark changed nested identities", got, nested)
			}
			s.exec("ROLLBACK TO original", 0)
			s.unchanged(old, identity, definitions)
			if got := s.nestedIdentity(); !reflect.DeepEqual(got, nested) {
				s.t.Fatal("repeated rollback changed nested identities", got, nested)
			}
			s.heldRead(held, ids("v", 1, 2))
			s.exec("RELEASE original", 0)
			finish(true)
			s.unchanged(old, identity, definitions)
			if got := s.nestedIdentity(); !reflect.DeepEqual(got, nested) {
				s.t.Fatal("commit resurrected discarded nested identities", got, nested)
			}
			invalidDormant(s)
		})
	})
	for _, recovery := range []string{"savepoint", "full_rollback"} {
		t.Run("conversion_failure/"+recovery, func(t *testing.T) {
			routes(t, func(s *scenario, mode string) {
				for _, query := range []string{
					"CREATE TYPE oldenum AS ENUM ('active','suspended')",
					"CREATE TABLE t(id INT,state oldenum)",
					"CREATE VIEW v AS SELECT id FROM t",
					"CREATE VIEW w AS SELECT id FROM v",
					"CREATE TABLE marker(id INT)",
					"CREATE VIEW j AS SELECT w.id FROM w JOIN marker ON w.id=marker.id",
				} {
					s.exec(query, 0)
				}
				s.exec("INSERT INTO t VALUES(1,'active'),(2,'suspended'),(3,NULL)", 3)
				s.exec("INSERT INTO marker VALUES(1),(2),(3)", 3)
				old, identity, definitions := s.oid("oldenum"), s.identity(), s.definitions()
				nested := s.nestedIdentity()
				held := s.prepare(s.conn, ids("v").Query)
				finish := s.begin(mode)
				s.exec("SAVEPOINT conversion", 0)
				s.exec("CREATE TYPE replacement AS ENUM ('active','dormant')", 0)
				s.failure("ALTER TABLE t ALTER state TYPE replacement USING state::text::replacement", "22P02", `invalid input value for enum replacement: "suspended"`, "", "")
				s.aborted(held, ids("v").Query)
				if recovery == "savepoint" {
					s.exec("ROLLBACK TO conversion", 0)
				} else {
					finish(false)
				}
				if s.oid("oldenum") != old {
					s.t.Fatal("failed conversion changed old enum")
				}
				s.sameIdentity(identity)
				if got := s.nestedIdentity(); !reflect.DeepEqual(got, nested) {
					s.t.Fatal("failed conversion changed nested identities", got, nested)
				}
				if got := s.definitions(); !reflect.DeepEqual(got, definitions) {
					s.t.Fatal("failed conversion changed definitions", got, definitions)
				}
				s.links()
				s.graph(1, 2, 3)
				s.heldRead(held, ids("v", 1, 2, 3))
				s.run(fixture.Select("SELECT id,state::text FROM t ORDER BY id", []string{"id", "state"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "active"}, {int64(2), "suspended"}, {int64(3), nil}}))
				s.run(fixture.Select("SELECT count(*) FROM pg_type WHERE typname='replacement'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				if recovery == "savepoint" {
					s.exec("RELEASE conversion", 0)
					finish(true)
				}
				// Healthy subsequent conversion proves the failed prefix left no
				// poisoned staged schema, converted first row, or dependency edit.
				s.exec("DELETE FROM t WHERE id=2", 1)
				finish = s.begin(mode)
				s.replacement()
				finish(true)
				s.exec("INSERT INTO t VALUES(4,'dormant')", 1)
				s.heldRead(held, ids("v", 1, 3, 4))
			})
		})
	}
}

func invalidDormant(s *scenario) {
	s.t.Helper()
	query := "INSERT INTO t VALUES(9,'dormant')"
	step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22P02", Message: `invalid input value for enum oldenum: "dormant"`, Position: strconv.Itoa(strings.Index(query, "'dormant'") + 1)})
	step.ErrorStage = "execute"
	if !s.held {
		s.run(step)
		return
	}
	// PostgreSQL diagnoses an invalid enum literal while preparing it; unlike
	// the runtime conversion failure this is not an executable held utility.
	step.Prepared, step.ErrorStage = true, "prepare"
	s.step(s.e, step)
}
