package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCreateroleSelfGrantFeatureSmoke(t *testing.T) {
	selfGrantCases(t, "sg_smoke", []string{"off", "on", "peer"}, func(s *selfGrantCase) {
		s.reads(s.actor, "", "default")
		off := s.create("off")
		s.edges(s.actor, off, false, false, false)
		s.set(s.actor, "set, inherit")
		s.reads(s.actor, "set, inherit", "session")
		on := s.create("on")
		s.edges(s.actor, on, true, true, true)
		for _, tc := range []struct{ name string; inherit, set bool }{{off, false, false}, {on, true, true}} {
			for _, inquiry := range []struct{ kind string; want bool }{{"MEMBER", true}, {"USAGE", tc.inherit}, {"SET", tc.set}} {
				s.run(s.actor, fixture.Select("SELECT pg_has_role(" + selfGrantLiteral(s.creator) + "," + selfGrantLiteral(tc.name) + "," + selfGrantLiteral(inquiry.kind) + ")", []string{"pg_has_role"}, []string{"BOOL"}, [][]any{{inquiry.want}}))
			}
		}
		s.run(s.actor, fixture.RoleReferenceError("SET ROLE " + off, "42501", `permission denied to set role "` + off + `"`, "", false, "execute"))
		s.exec(s.actor, "SET ROLE " + on)
		s.run(s.actor, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{on}}))
		s.exec(s.actor, "RESET ROLE")
		s.run(s.actor, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{s.creator}}))
		s.exec(s.admin, "GRANT USAGE ON SCHEMA public TO " + s.creator)
		for _, name := range []string{off, on} {
			s.exec(s.admin, "CREATE TABLE " + name + "_data(id INT)")
			s.run(s.admin, fixture.Exec("INSERT INTO " + name + "_data VALUES(7)", 1))
			s.exec(s.admin, "GRANT USAGE ON SCHEMA public TO " + name)
			s.exec(s.admin, "GRANT SELECT ON " + name + "_data TO " + name)
		}
		defer s.exec(s.admin, "DROP TABLE " + off + "_data," + on + "_data")
		s.run(s.actor, fixture.RoleReferenceError("SELECT id FROM " + off + "_data", "42501", "permission denied for table " + off + "_data", "", false, "execute"))
		s.run(s.actor, fixture.Select("SELECT id FROM " + on + "_data", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		s.exec(s.actor, "RESET createrole_self_grant")
		s.reads(s.actor, "", "default")
		s.edges(s.actor, on, true, true, true)
		s.run(s.actor, fixture.Select("SELECT id FROM " + on + "_data", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		s.set(s.actor, "set,inherit")
		s.reads(s.peer, "", "default")
		s.exec(s.peer, "CREATE ROLE sg_smoke_peer")
		s.edges(s.peer, "sg_smoke_peer", false, false, false)
		s.reads(s.actor, "set,inherit", "session")
	})
}
