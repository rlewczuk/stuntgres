package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCreateroleSelfGrantOptions(t *testing.T) {
	suffixes := []string{"su", "noinherit", "parent", "member", "admin", "initial", "bad"}
	for i := range 8 { suffixes = append(suffixes, fmt.Sprintf("opt%d", i)) }
	selfGrantCases(t, "sg_options", suffixes, func(s *selfGrantCase) {
		for i, tc := range []struct{ text string; inherit, set bool }{
			{"", false, false}, {"   ", false, false}, {"set", false, true}, {"inherit", true, false},
			{"inherit,set", true, true}, {"SET,INHERIT,SET", true, true}, {`"INHERIT", "set"`, true, true}, {"  SET ,inherit ", true, true},
		} {
			s.set(s.actor, tc.text)
			name := s.create(fmt.Sprintf("opt%d", i))
			s.edges(s.actor, name, tc.inherit, tc.set, tc.inherit || tc.set)
			s.reads(s.actor, tc.text, "session")
		}
		s.set(s.admin, "set,inherit")
		s.exec(s.admin, "CREATE ROLE sg_options_su")
		s.run(s.admin, fixture.Select("SELECT a.admin_option FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid WHERE r.rolname='sg_options_su'", []string{"admin_option"}, []string{"BOOL"}, nil))
		s.exec(s.admin, "ALTER ROLE " + s.creator + " NOINHERIT")
		s.set(s.actor, "inherit")
		noinherit := s.create("noinherit")
		s.edges(s.actor, noinherit, true, false, true)
		// Explicit INHERIT overrides the creator's NOINHERIT default on the edge.
		s.exec(s.admin, "CREATE TABLE sg_options_owned(id INT)")
		defer s.exec(s.admin, "DROP TABLE sg_options_owned")
		s.run(s.admin, fixture.Exec("INSERT INTO sg_options_owned VALUES(7)", 1))
		s.exec(s.admin, "GRANT USAGE ON SCHEMA public TO " + s.creator)
		s.exec(s.admin, "GRANT SELECT ON sg_options_owned TO " + noinherit)
		s.run(s.actor, fixture.Select("SELECT id FROM sg_options_owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		s.exec(s.admin, "ALTER ROLE " + s.creator + " INHERIT")
		s.set(s.actor, "set,inherit")
		s.exec(s.actor, "ALTER ROLE " + noinherit + " LOGIN")
		s.edges(s.actor, noinherit, true, false, true)
		for _, suffix := range []string{"parent", "member", "admin"} { s.exec(s.admin, "CREATE ROLE sg_options_" + suffix) }
		s.exec(s.admin, "GRANT sg_options_parent TO " + s.creator + " WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		s.exec(s.actor, "CREATE ROLE sg_options_initial IN ROLE sg_options_parent ROLE sg_options_member ADMIN sg_options_admin")
		s.edges(s.actor, "sg_options_initial", true, true, true)
		q := "SELECT r.rolname,m.rolname,a.admin_option,a.inherit_option,a.set_option,g.rolname FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid JOIN pg_roles m ON m.oid=a.member JOIN pg_roles g ON g.oid=a.grantor WHERE (r.rolname='sg_options_initial' OR m.rolname='sg_options_initial') AND m.rolname<>" + selfGrantLiteral(s.creator)
		step := fixture.Select(q, []string{"rolname", "rolname", "admin_option", "inherit_option", "set_option", "rolname"}, []string{"NAME", "NAME", "BOOL", "BOOL", "BOOL", "NAME"}, [][]any{
			{"sg_options_parent", "sg_options_initial", false, true, true, s.creator},
			{"sg_options_initial", "sg_options_member", false, true, true, s.creator},
			{"sg_options_initial", "sg_options_admin", true, true, true, s.creator},
		})
		step.Bag = true
		s.run(s.actor, step)
		s.run(s.actor, fixture.RoleReferenceError("CREATE ROLE sg_options_bad IN ROLE sg_options_parent ROLE sg_options_member ADMIN sg_options_absent", "42704", `role "sg_options_absent" does not exist`, "", false, "execute"))
		s.absent(s.actor, "sg_options_bad")
		s.create("bad")
		s.edges(s.actor, "sg_options_bad", true, true, true)
	})
}
