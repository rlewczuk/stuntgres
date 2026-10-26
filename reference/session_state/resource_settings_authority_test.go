package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceResourceSettingsAuthority(t *testing.T) {
	physicalCases(t,[]string{"resource_authority_login","resource_authority_grantee","resource_authority_super"},func(s *physicalCase) {
		for _, command := range []string{"CREATE ROLE resource_authority_login LOGIN PASSWORD 'role-core-fixture'","CREATE ROLE resource_authority_grantee","CREATE ROLE resource_authority_super SUPERUSER"} { s.run(fixture.Exec(command,0)) }
		ordinary := s.login("resource_authority_login")
		for _, m := range resourceMutations {
			if resourceDefinition(m.name).context != "user" { continue }
			for _, prepared := range []bool{false,true} { step:=fixture.Exec("SET "+m.name+"="+physicalLiteral(m.input),0); step.Prepared=prepared; ordinary.run(step); ordinary.resourceValue(m.name,m.show,m.raw,"session",prepared) }
		}
		ordinary.run(fixture.Exec("RESET ALL",0))
		for _, name := range []string{"max_stack_depth","temp_file_limit"} {
			denied := clientSetDenied(name)
			deny := func() {
				for _, prepared := range []bool{false,true} {
					for _, query := range []string{"SET "+name+"='bogus'","SET "+name+"=DEFAULT","RESET "+name} { ordinary.run(physicalError(query,denied,prepared,false)) }
					ordinary.run(physicalError("SELECT set_config($1,$2,false)",denied,prepared,true,name,"bogus"))
				}
			}
			deny()
			s.run(fixture.Exec("GRANT pg_read_all_settings TO resource_authority_login",0)); deny()
			s.run(fixture.Exec("GRANT resource_authority_super TO resource_authority_login",0)); deny()
			// Membership alone is not effective superuser; SET ROLE is.
			ordinary.run(fixture.Exec("SET ROLE resource_authority_super",0))
			ordinary.run(fixture.Exec("SET "+name+"='1024'",0))
			ordinary.run(fixture.Exec("RESET ROLE",0))
			ordinary.run(fixture.Exec("RESET ALL",0))
			query := "SET "+name+"='1024'"
			held,err := ordinary.conn.PrepareContext(s.p.Ctx,query)
			if err != nil { s.t.Fatal(err) }
			execute := func(step fixture.Step) { fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:ordinary.conn,Statement:held,SQL:query},ordinary.recorder,step) }
			execute(physicalError(query,denied,false,false))
			for _, grantee := range []string{"resource_authority_login","resource_authority_grantee"} {
				if grantee == "resource_authority_grantee" { s.run(fixture.Exec("GRANT resource_authority_grantee TO resource_authority_login WITH INHERIT TRUE",0)) }
				s.run(fixture.Exec("GRANT SET ON PARAMETER "+name+" TO "+grantee,0))
				execute(fixture.Exec(query,0))
				ordinary.resourceValue(name,"1MB","1024","session",true)
				s.run(fixture.Exec("REVOKE SET ON PARAMETER "+name+" FROM "+grantee,0))
				execute(physicalError(query,denied,false,false))
				ordinary.run(physicalError("RESET "+name,denied,true,false))
				ordinary.run(fixture.Exec("BEGIN",0)); ordinary.run(fixture.Exec("RESET ALL",0)); ordinary.run(fixture.Exec("ROLLBACK",0))
				ordinary.resourceValue(name,"1MB","1024","session",false)
				ordinary.run(fixture.Exec("RESET ALL",0))
				d:=resourceDefinition(name); raw,show,source:=ordinary.resourceInitial(d); ordinary.resourceValue(name,show,raw,source,true)
			}
			held.Close()
		}
	})
}
