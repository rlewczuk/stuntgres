package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGParameterACLGrantedEntries(t *testing.T) {
	pgParameterACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgParameterACLRoles(t, p, b, "pacl18_recipient", "pacl18_delegate", "pacl18_observer")
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		defer func() {
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "RESET application_name", "REVOKE ALL ON PARAMETER lc_messages FROM PUBLIC", "DROP OWNED BY pacl18_recipient CASCADE", "DROP OWNED BY pacl18_delegate CASCADE"} {
				if _, err := conn.ExecContext(p.Ctx, q); err != nil {
					t.Error("parameter ACL cleanup", q, err)
				}
			}
		}()
		owner := pgParameterACLOwner(b)
		prefix := "{" + owner + "=sA/" + owner + ",pacl18_recipient="
		var oid int64
		for _, tc := range []struct{ command, rights string }{
			{`GRANT SET ON PARAMETER "LC_MESSAGES" TO pacl18_recipient`, "s"},
			{"GRANT ALTER SYSTEM ON PARAMETER lc_messages TO pacl18_recipient", "sA"},
			{"GRANT SET ON PARAMETER lc_messages TO pacl18_recipient WITH GRANT OPTION", "s*A"},
			{"GRANT ALTER SYSTEM ON PARAMETER lc_messages TO pacl18_recipient WITH GRANT OPTION", "s*A*"},
		} {
			run(fixture.Exec(tc.command, 0))
			actual := pgParameterACLOID(t, p, b, "lc_messages")
			if oid != 0 && oid != actual {
				t.Fatal("grant update replaced identity", oid, actual)
			}
			oid = actual
			pgParameterACLRead(t, p, b, "lc_messages", prefix+tc.rights+"/"+owner+"}", oid)
		}
		run(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO PUBLIC", 0))
		acl := prefix + "s*A*/" + owner + ",=s/" + owner + "}"
		pgParameterACLRead(t, p, b, "lc_messages", acl, oid)
		fixture.RoleGrantActor(run, "pacl18_recipient", func() {
			run(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO pacl18_delegate", 0))
		})
		acl = prefix + "s*A*/" + owner + ",=s/" + owner + ",pacl18_delegate=s/pacl18_recipient}"
		pgParameterACLRead(t, p, b, "lc_messages", acl, oid)
		fixture.RoleGrantActor(run, "pacl18_observer", func() {
			s := pgParameterACLSelect("SELECT * FROM pg_parameter_acl WHERE parname='lc_messages'", b.PG, [][]any{{oid, "lc_messages", acl}})
			run(s)
			run(fixture.Prepared(s))
		})
		run(fixture.Select("SELECT parname FROM pg_parameter_acl WHERE parname='LC_MESSAGES'", []string{"parname"}, []string{"TEXT"}, nil))
		run(fixture.Exec("SET application_name='pacl18_no_row'", 0))
		run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"pacl18_no_row"}}))
		run(pgParameterACLSelect("SELECT * FROM pg_parameter_acl WHERE parname='application_name'", b.PG, nil))
		run(fixture.Exec("GRANT SET ON PARAMETER application_name TO pacl18_recipient", 0))
		pgParameterACLRead(t, p, b, "application_name", prefix+"s/"+owner+"}", pgParameterACLOID(t, p, b, "application_name"))
		run(fixture.Exec(`GRANT SET ON PARAMETER "PACL18_ENTRIES"."MiXeD" TO pacl18_recipient`, 0))
		pgParameterACLRead(t, p, b, "pacl18_entries.mixed", prefix+"s/"+owner+"}", pgParameterACLOID(t, p, b, "pacl18_entries.mixed"))
	})
}

func TestReferencePGParameterACLExplicitEmpty(t *testing.T) {
	pgParameterACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		const name = "pacl18_empty.value"
		defer func() {
			for _, q := range []string{"GRANT ALL ON PARAMETER " + name + " TO CURRENT_USER", "REVOKE ALL ON PARAMETER " + name + " FROM PUBLIC"} {
				if _, err := b.DB.ExecContext(p.Ctx, q); err != nil {
					t.Error("restore bootstrap parameter privileges", err)
				}
			}
		}()
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT rolname FROM pg_roles WHERE oid=10::oid", []string{"rolname"}, []string{"NAME"}, [][]any{{pgParameterACLOwner(b)}}))
		// PostgreSQL does not create an absent ACL tuple for REVOKE. Seed it
		// through a real grant before removing the owner and PUBLIC entries.
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("GRANT SET ON PARAMETER "+name+" TO PUBLIC", 0))
		seedOID := pgParameterACLOID(t, p, b, name)
		owner := pgParameterACLOwner(b)
		pgParameterACLRead(t, p, b, name, "{"+owner+"=sA/"+owner+",=s/"+owner+"}", seedOID)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("REVOKE ALL ON PARAMETER "+name+" FROM CURRENT_USER", 0))
		pgParameterACLRead(t, p, b, name, "{=s/"+owner+"}", seedOID)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("REVOKE ALL ON PARAMETER "+name+" FROM PUBLIC", 0))
		if id := pgParameterACLOID(t, p, b, name); id != seedOID {
			t.Fatalf("explicit empty changed parameter identity: got %v, want %v", id, seedOID)
		}
		pgParameterACLRead(t, p, b, name, "{}", seedOID)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("GRANT ALL ON PARAMETER "+name+" TO CURRENT_USER", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, pgParameterACLSelect("SELECT * FROM pg_parameter_acl WHERE parname=$1", b.PG, nil, name))
	})
}
