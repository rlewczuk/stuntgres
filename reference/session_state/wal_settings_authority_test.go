package reference_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func (s *physicalCase) walRevoke(query string) {
	s.t.Helper()
	ctx,cancel := context.WithTimeout(context.Background(),5*time.Second)
	defer cancel()
	if _,err := s.conn.ExecContext(ctx,query); err != nil { s.t.Error(err) }
}

func TestReferenceWALSettingsAuthority(t *testing.T) {
	for _, name := range []string{"commit_delay","wal_compression","wal_init_zero","wal_recycle"} {
		t.Run(name,func(t *testing.T) {
			roles := []string{"wal_auth_plain","wal_auth_reader","wal_auth_setter","wal_auth_inherited","wal_auth_alter","wal_auth_super"}
			physicalCases(t,roles,func(s *physicalCase) {
				for _, role := range roles {
					attributes := " LOGIN PASSWORD 'role-core-fixture'"; if role == "wal_auth_super" { attributes += " SUPERUSER" }
					s.run(fixture.Exec("CREATE ROLE "+role+attributes,0))
				}
				s.run(fixture.Exec("GRANT pg_read_all_settings TO wal_auth_reader",0))
				s.run(fixture.Exec("GRANT SET ON PARAMETER "+name+" TO wal_auth_setter",0))
				s.run(fixture.Exec("GRANT wal_auth_setter TO wal_auth_inherited",0))
				s.run(fixture.Exec("GRANT ALTER SYSTEM ON PARAMETER "+name+" TO wal_auth_alter",0))
				defer func() {
					ctx,cancel := context.WithTimeout(context.Background(),5*time.Second); defer cancel()
					for _, query := range []string{"REVOKE ALL PRIVILEGES ON PARAMETER "+name+" FROM wal_auth_setter,wal_auth_plain,wal_auth_alter"} {
						if _,err := s.conn.ExecContext(ctx,query); err != nil { t.Error(err) }
					}
				}()
				d := walDefinition(name)
				var change struct{ name,input,show,raw string }; for _, m := range walMutable { if m.name == name { change = m } }
				for _, role := range roles {
					user := s.login(role)
					for _, prepared := range []bool{false,true} {
						user.walInventory(prepared,role == "wal_auth_super" || role == "wal_auth_reader")
						if role == "wal_auth_setter" || role == "wal_auth_inherited" || role == "wal_auth_super" {
							user.walSet(name,change.input,prepared); user.walValue(name,change.show,change.raw,"session",prepared)
							step := fixture.Exec("RESET "+name,0); step.Prepared = prepared; user.run(step)
						} else {
							for _, query := range []string{"SET "+name+"='invalid'","SET "+name+"=DEFAULT","RESET "+name,"SELECT set_config('"+name+"','invalid',false)","SELECT set_config('"+name+"',NULL,true)"} {
								user.run(physicalError(query,walDenied(name),prepared,strings.HasPrefix(query,"SELECT")))
							}
						}
						user.walValue(name,d.show,d.raw,"default",prepared)
						user.walSet("commit_siblings","2",prepared); user.run(fixture.Exec("RESET ALL",0))
					}
					user.conn.Close()
				}
				plain := s.login("wal_auth_plain")
				query := "SELECT set_config($1,$2,false)"
				stmt,err := plain.conn.PrepareContext(s.p.Ctx,query); if err != nil { t.Fatal(err) }; defer stmt.Close()
				held := fixture.HeldStatement{Executor:plain.conn,Statement:stmt,SQL:query}
				attempt := func(allowed bool) {
					step := physicalError(query,walDenied(name),false,true,name,change.input)
					if allowed { step = fixture.Select(query,[]string{"set_config"},[]string{"TEXT"},[][]any{{change.show}},name,change.input) }
					fixture.RunStep(t,s.p.Ctx,held,plain.recorder,step)
				}
				attempt(false)
				s.run(fixture.Exec("GRANT SET ON PARAMETER "+name+" TO wal_auth_plain",0)); attempt(true)
				s.run(fixture.Exec("REVOKE SET ON PARAMETER "+name+" FROM wal_auth_plain",0)); attempt(false)
				plain.walValue(name,change.show,change.raw,"session",true)
				plain.run(physicalError("RESET "+name,walDenied(name),true,false))
				plain.run(physicalError("SET "+name+"=DEFAULT",walDenied(name),true,false))
				plain.run(fixture.Exec("RESET ALL",0)); plain.walValue(name,d.show,d.raw,"default",true)
				// A prepared mutation uses the effective role at execution, not
				// bootstrap authority captured when prepared.
				adminStmt,err := s.conn.PrepareContext(s.p.Ctx,query); if err != nil { t.Fatal(err) }; defer adminStmt.Close()
				adminHeld := fixture.HeldStatement{Executor:s.conn,Statement:adminStmt,SQL:query}
				s.run(fixture.Exec("SET ROLE wal_auth_plain",0))
				fixture.RunStep(t,s.p.Ctx,adminHeld,s.recorder,physicalError(query,walDenied(name),false,true,name,change.input))
				s.run(fixture.Exec("SET ROLE wal_auth_setter",0))
				fixture.RunStep(t,s.p.Ctx,adminHeld,s.recorder,fixture.Select(query,[]string{"set_config"},[]string{"TEXT"},[][]any{{change.show}},name,change.input))
				s.run(fixture.Exec("RESET ROLE",0)); s.run(fixture.Exec("RESET ALL",0))
			})
		})
	}
}
