package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceResourceSettingsLifecycle(t *testing.T) {
	for _, m := range resourceMutations {
		t.Run(m.name,func(t *testing.T) {
			physicalCases(t,nil,func(s *physicalCase) {
				peer:=s.lockPeer()
				d:=resourceDefinition(m.name); raw,show,source:=s.resourceInitial(d)
				query:="SELECT current_setting("+physicalLiteral(m.name)+")"
				held,err:=s.conn.PrepareContext(s.p.Ctx,query); if err!=nil { s.t.Fatal(err) }; defer held.Close()
				check:=func(assigned bool) {
					v,r,origin:=show,raw,source; if assigned { v,r,origin=m.show,m.raw,"session" }
					s.resourceValue(m.name,v,r,origin,true)
					fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:held,SQL:query},s.recorder,clientText(query,"current_setting",v))
					peer.resourceValue(m.name,show,raw,source,false)
				}
				set:="SET "+m.name+"="+physicalLiteral(m.input)
				for _, tc:=range []struct{commands []string; assigned bool}{
					{[]string{"BEGIN",set,"ROLLBACK"},false},
					{[]string{"BEGIN",set,"COMMIT"},true},
					{[]string{"RESET "+m.name,"BEGIN","SET LOCAL "+m.name+"="+physicalLiteral(m.input)},true},
					{[]string{"COMMIT"},false},
					{[]string{"BEGIN","SET LOCAL "+m.name+"="+physicalLiteral(m.input),"ROLLBACK"},false},
					{[]string{"BEGIN","SAVEPOINT resource_save",set,"ROLLBACK TO resource_save","RELEASE resource_save","COMMIT"},false},
					{[]string{"BEGIN","SAVEPOINT resource_save","SET LOCAL "+m.name+"="+physicalLiteral(m.input),"RELEASE resource_save"},true},
					{[]string{"COMMIT"},false},
				} { for _, command:=range tc.commands { s.run(fixture.Exec(command,0)) }; check(tc.assigned) }
				for _, prepared:=range []bool{false,true} {
					for _, local:=range []bool{false,true} {
						s.run(fixture.Exec("BEGIN",0))
						step:=fixture.Select("SELECT set_config($1,$2,$3)",[]string{"set_config"},[]string{"TEXT"},[][]any{{m.show}},m.name,m.input,local); step.Prepared=prepared; s.run(step); check(true)
						s.run(fixture.Exec("COMMIT",0)); check(!local)
						s.run(fixture.Exec("RESET "+m.name,0)); check(false)
					}
					for _, reset:=range []string{"SET "+m.name+"=DEFAULT","RESET "+m.name,"RESET ALL","SELECT set_config($1,NULL,false)"} {
						s.run(fixture.Exec(set,0))
						step:=fixture.Exec(reset,0)
						if reset=="SELECT set_config($1,NULL,false)" { step=fixture.Select(reset,[]string{"set_config"},[]string{"TEXT"},[][]any{{show}},m.name) }
						step.Prepared=prepared; s.run(step); check(false)
					}
				}
			})
		})
	}
	t.Run("temp-initialized-count",resourceTemporaryLifecycle)
	t.Run("native-independent-temp-source-initialization",resourceTemporarySourceInitialization)
	t.Run("initialized-temp-validation-ordering",resourceTemporaryInitializedValidation)
	t.Run("temp-streams-and-foreign-attempts",resourceTemporaryStreams)
	t.Run("native-controls",resourceNativeControls)
}

func resourceTemporaryLifecycle(t *testing.T) {
	for _, prepared:=range []bool{false,true} {
		physicalCases(t,nil,func(s *physicalCase) {
			peer:=s.lockPeer()
			s.run(fixture.Exec("CREATE TEMP TABLE resource_temp(v integer CHECK(v>0))",0))
			held,err:=s.conn.PrepareContext(s.p.Ctx,"SELECT v FROM resource_temp"); if err!=nil { s.t.Fatal(err) }; held.Close()
			// CREATE and Prepare have not initialized storage access.
			s.run(fixture.Exec("SET temp_buffers=2048",0))
			s.run(fixture.Exec("BEGIN",0)); s.run(fixture.Exec("SAVEPOINT temp_capture",0))
			step:=fixture.Exec("INSERT INTO resource_temp VALUES(11)",1); step.Prepared=prepared; s.run(step)
			s.run(fixture.Exec("ROLLBACK TO temp_capture",0)); s.run(fixture.Exec("ROLLBACK",0))
			// The count survives rollback even though the row did not.
			for _, query:=range []string{"SET temp_buffers='1024'","SELECT set_config('temp_buffers','1024',false)"} { s.run(physicalError(query,resourceTempError("1024"),prepared,query[:6]=="SELECT")) }
			s.run(fixture.Exec("RESET temp_buffers",0)); s.resourceValue("temp_buffers","8MB","1024","default",true)
			s.run(fixture.Exec("SET temp_buffers='16MB'",0)); s.resourceValue("temp_buffers","16MB","2048","session",true)
			s.run(physicalError("SET temp_buffers='1024'",resourceTempError("1024"),prepared,false))
			s.run(physicalError("SET temp_buffers='8MB'",resourceTempError("1024"),prepared,false))
			for _, reset:=range []string{"SET temp_buffers=DEFAULT","RESET ALL"} { s.run(fixture.Exec(reset,0)); s.resourceValue("temp_buffers","8MB","1024","default",false); s.run(fixture.Exec("SET temp_buffers=2048",0)) }
			s.run(fixture.Select("SELECT set_config('temp_buffers',NULL,false)",[]string{"set_config"},[]string{"TEXT"},[][]any{{"8MB"}}))
			s.run(fixture.Exec("BEGIN",0)); s.run(fixture.Exec("SET LOCAL temp_buffers=2048",0)); s.run(fixture.Exec("COMMIT",0)); s.resourceValue("temp_buffers","8MB","1024","default",true)
			s.run(fixture.Exec("DROP TABLE resource_temp",0)); s.run(fixture.Exec("CREATE TEMP TABLE resource_temp(v integer)",0))
			s.run(physicalError("SET temp_buffers='1024'",resourceTempError("1024"),prepared,false))
			s.run(fixture.Exec("DISCARD TEMP",0))
			s.run(physicalError("SET temp_buffers='1024'",resourceTempError("1024"),prepared,false))
			peer.run(fixture.Exec("SET temp_buffers=3072",0)); peer.resourceValue("temp_buffers","24MB","3072","session",true)
		})
	}
	// These populated shared cases prove preservation across later data access.
	// Independent first initialization is covered by fresh empty native owners.
	for _, operation:=range []string{"SELECT v FROM resource_temp_path","UPDATE resource_temp_path SET v=12","DELETE FROM resource_temp_path"} {
		t.Run(operation,func(t *testing.T) {
			physicalCases(t,nil,func(s *physicalCase) {
				s.run(fixture.Exec("CREATE TEMP TABLE resource_temp_path(v integer)",0))
				s.run(fixture.Exec("SET temp_buffers=2048",0)); s.run(fixture.Exec("INSERT INTO resource_temp_path VALUES(11)",1))
				if operation=="SELECT v FROM resource_temp_path" { s.run(fixture.Select(operation,[]string{"v"},[]string{"INT4"},[][]any{{int64(11)}}))
				} else { s.run(fixture.Exec(operation,1)) }
				s.run(physicalError("SET temp_buffers='1024'",resourceTempError("1024"),true,false))
			})
		})
	}
}
