package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCreateroleSelfGrantPrepared(t *testing.T) {
	selfGrantCases(t, "sg_held", []string{"on", "off", "denied", "ordinary", "other", "current"}, func(s *selfGrantCase) {
		t := s.t
		s.reads(s.actor, "", "default")
		show, err := s.actor.PrepareContext(s.p.Ctx, "SHOW createrole_self_grant")
		if err != nil { t.Fatal(err) }
		defer show.Close()
		current, err := s.actor.PrepareContext(s.p.Ctx, "SELECT current_setting('createrole_self_grant')")
		if err != nil { t.Fatal(err) }
		defer current.Close()
		for _, tc := range []struct{ name, before, after string; enabled bool }{
			{"sg_held_on", "", "set,inherit", true},
			{"sg_held_off", "set,inherit", "", false},
		} {
			s.set(s.actor, tc.before)
			query := "CREATE ROLE " + tc.name
			held, err := s.actor.PrepareContext(s.p.Ctx, query)
			if err != nil { t.Fatal(err) }
			s.absent(s.actor, tc.name)
			s.reads(s.actor, tc.before, "session")
			s.set(s.actor, tc.after)
			fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.actor, Statement: held, SQL: query}, s.backend.Recorder, fixture.Exec(query, 0))
			held.Close()
			s.edges(s.actor, tc.name, tc.enabled, tc.enabled, tc.enabled)
			fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.actor, Statement: show, SQL: "SHOW createrole_self_grant"}, s.backend.Recorder, fixture.Select("SHOW createrole_self_grant", []string{"createrole_self_grant"}, []string{"TEXT"}, [][]any{{tc.after}}))
			fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.actor, Statement: current, SQL: "SELECT current_setting('createrole_self_grant')"}, s.backend.Recorder, fixture.Select("SELECT current_setting('createrole_self_grant')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{tc.after}}))
		}
		denied, err := s.actor.PrepareContext(s.p.Ctx, "CREATE ROLE sg_held_denied")
		if err != nil { t.Fatal(err) }
		defer denied.Close()
		s.exec(s.admin, "ALTER ROLE " + s.creator + " NOCREATEROLE")
		want := selfGrantDenied("CREATE ROLE sg_held_denied", "Only roles with the CREATEROLE attribute may create roles.")
		fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.actor, Statement: denied, SQL: want.Query}, s.backend.Recorder, want)
		s.absent(s.actor, "sg_held_denied")
		ordinary, err := s.actor.PrepareContext(s.p.Ctx, "CREATE ROLE sg_held_ordinary")
		if err != nil { t.Fatal(err) }
		defer ordinary.Close()
		want = selfGrantDenied("CREATE ROLE sg_held_ordinary", "Only roles with the CREATEROLE attribute may create roles.")
		fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.actor, Statement: ordinary, SQL: want.Query}, s.backend.Recorder, want)
		s.absent(s.actor, "sg_held_ordinary")
		s.exec(s.admin, "ALTER ROLE " + s.creator + " CREATEROLE")
		s.exec(s.admin, "CREATE ROLE sg_held_other CREATEROLE")
		s.exec(s.admin, "GRANT sg_held_other TO " + s.creator + " WITH INHERIT FALSE, SET TRUE")
		held, err := s.actor.PrepareContext(s.p.Ctx, "CREATE ROLE sg_held_current")
		if err != nil { t.Fatal(err) }
		defer held.Close()
		s.set(s.actor, "inherit,set")
		s.exec(s.actor, "SET ROLE sg_held_other")
		fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.actor, Statement: held, SQL: "CREATE ROLE sg_held_current"}, s.backend.Recorder, fixture.Exec("CREATE ROLE sg_held_current", 0))
		original := s.creator
		s.creator = "sg_held_other"
		s.edges(s.actor, "sg_held_current", true, true, true)
		s.creator = original
		s.run(s.actor, fixture.Select("SELECT a.admin_option FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid JOIN pg_roles m ON m.oid=a.member WHERE r.rolname='sg_held_current' AND m.rolname=" + selfGrantLiteral(original), []string{"admin_option"}, []string{"BOOL"}, nil))
		s.exec(s.actor, "RESET ROLE")
		s.reads(s.actor, "inherit,set", "session")
	})
}
