package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCreateroleSelfGrantPresentationAndErrors(t *testing.T) {
	selfGrantCases(t, "sg_display", []string{"retained", "denied", "super"}, func(s *selfGrantCase) {
		s.reads(s.actor, "", "default")
		for _, text := range []string{"", "   ", "SET", "inherit", "inherit,set", "  SET ,inherit ", "SET,SET", `"SeT"`, `"INHERIT", "set"`} {
			s.set(s.actor, text)
			s.reads(s.actor, text, "session")
			s.run(s.actor, fixture.Select("SELECT set_config('createrole_self_grant'," + selfGrantLiteral(text) + ",false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{text}}))
			s.reads(s.actor, text, "session")
		}
		s.exec(s.actor, "SET createrole_self_grant TO SET, INHERIT")
		s.reads(s.actor, "set, inherit", "session")
		for _, tc := range []struct{ text, detail string }{
			{"bogus", `Unrecognized key word: "bogus".`}, {"admin", `Unrecognized key word: "admin".`}, {`""`, `Unrecognized key word: "".`},
			{"SET,", "List syntax is invalid."}, {",SET", "List syntax is invalid."}, {"SET,,INHERIT", "List syntax is invalid."},
			{`"SET`, "List syntax is invalid."}, {"SET INHERIT", "List syntax is invalid."}, {"bogus,SET,", "List syntax is invalid."},
		} {
			s.run(s.actor, selfGrantInvalid(tc.text, tc.detail))
			s.reads(s.actor, "set, inherit", "session")
		}
		invalidCall := selfGrantInvalid("bogus", `Unrecognized key word: "bogus".`)
		invalidCall.Query = "SELECT set_config('createrole_self_grant','bogus',false)"
		s.run(s.actor, invalidCall)
		s.reads(s.actor, "set, inherit", "session")
		s.create("retained")
		s.edges(s.actor, "sg_display_retained", true, true, true)
		for _, reset := range []string{"RESET createrole_self_grant", "SET createrole_self_grant TO DEFAULT", "RESET ALL"} {
			s.set(s.actor, "SET")
			s.exec(s.actor, reset)
			s.reads(s.actor, "", "default")
			s.edges(s.actor, "sg_display_retained", true, true, true)
		}
		s.run(s.actor, fixture.Exec("SHOW createrole_self_grant", 0))
		s.run(s.actor, fixture.Exec("SELECT current_setting('createrole_self_grant')", 1))
		s.run(s.actor, fixture.Exec("SELECT set_config('createrole_self_grant','inherit',false)", 1))
		s.reads(s.actor, "inherit", "session")
		s.run(s.actor, selfGrantDenied("CREATE ROLE sg_display_super SUPERUSER", "Only roles with the SUPERUSER attribute may create roles with the SUPERUSER attribute."))
		s.absent(s.actor, "sg_display_super")
		s.exec(s.admin, "ALTER ROLE " + s.creator + " NOCREATEROLE")
		// USERSET is not restricted to roles having CREATEROLE.
		s.set(s.actor, "set,inherit")
		s.reads(s.actor, "set,inherit", "session")
		s.run(s.actor, selfGrantDenied("CREATE ROLE sg_display_denied", "Only roles with the CREATEROLE attribute may create roles."))
		s.absent(s.actor, "sg_display_denied")
		s.reads(s.actor, "set,inherit", "session")
		s.reads(s.peer, "", "default")
	})
}
