package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeqoLifecycle(t *testing.T) {
	for _, d := range geqoSettings {
		t.Run(d.name, func(t *testing.T) {
			physicalCases(t, nil, func(s *physicalCase) {
				peer := s.lockPeer()
				query := "SELECT current_setting(" + physicalLiteral(d.name) + ")"
				held, err := s.conn.PrepareContext(s.p.Ctx, query)
				if err != nil { s.t.Fatal(err) }
				defer held.Close()
				metadataQuery := "SELECT setting,source,boot_val,reset_val FROM pg_settings WHERE name=" + physicalLiteral(d.name)
				metadata, err := s.conn.PrepareContext(s.p.Ctx, metadataQuery)
				if err != nil { s.t.Fatal(err) }
				defer metadata.Close()
				check := func(value, source string) {
					s.geqoValue(d, value, source, true)
					fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor:s.conn, Statement:held, SQL:query}, s.recorder, clientText(query,"current_setting",value))
					fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor:s.conn, Statement:metadata, SQL:metadataQuery}, s.recorder, fixture.Select(metadataQuery,[]string{"setting","source","boot_val","reset_val"},[]string{"TEXT","TEXT","TEXT","TEXT"},[][]any{{value,source,d.boot,d.boot}}))
					peer.geqoValue(d,d.boot,"default",false)
				}
				for _, prepared := range []bool{false,true} {
					command := func(query string) { step := fixture.Exec(query,0); step.Prepared = prepared; s.run(step) }
					set := "SET "+d.name+"="+physicalLiteral(d.a)
					local := "SET LOCAL "+d.name+"="+physicalLiteral(d.b)
					for _, end := range []string{"COMMIT","ROLLBACK"} {
						command("BEGIN"); command(set); check(d.a,"session"); command(end)
						if end == "COMMIT" { check(d.a,"session") } else { check(d.boot,"default") }
						command("RESET "+d.name); check(d.boot,"default")
						command("BEGIN"); command(local); check(d.b,"session"); command(end); check(d.boot,"default")
						command("BEGIN"); command(set); command(local); check(d.b,"session"); command(end)
						if end == "COMMIT" { check(d.a,"session") } else { check(d.boot,"default") }
						command("RESET "+d.name)
					}
					command("BEGIN"); command(set); command("SAVEPOINT geqo_outer"); command(local); check(d.b,"session")
					command("SAVEPOINT geqo_inner"); command(set); check(d.a,"session")
					command("ROLLBACK TO geqo_inner"); check(d.b,"session"); command("RELEASE geqo_inner"); check(d.b,"session")
					command("ROLLBACK TO geqo_outer"); check(d.a,"session"); command("RELEASE geqo_outer"); command("ROLLBACK"); check(d.boot,"default")
					command("BEGIN"); command("SAVEPOINT geqo_release"); command(local); command("RELEASE geqo_release"); check(d.b,"session"); command("COMMIT"); check(d.boot,"default")
					for _, localFlag := range []bool{false,true} {
						for _, end := range []string{"COMMIT","ROLLBACK"} {
							command("BEGIN")
							step := fixture.Select("SELECT set_config($1,$2,$3)",[]string{"set_config"},[]string{"TEXT"},[][]any{{d.a}},d.name,d.a,localFlag); step.Prepared = prepared; s.run(step); check(d.a,"session")
							command(end)
							if !localFlag && end == "COMMIT" { check(d.a,"session") } else { check(d.boot,"default") }
							command("RESET "+d.name)
						}
					}
					command("BEGIN"); command(local); command(set); command("COMMIT"); check(d.a,"session")
					v := geqoInvalidValues(d)[0]
					s.run(physicalError("SET "+d.name+"="+physicalLiteral(v.input),v.diagnostic,prepared,false)); check(d.a,"session")
					command("BEGIN"); command("SAVEPOINT geqo_failure")
					s.run(physicalError("SELECT set_config($1,$2,false)",v.diagnostic,prepared,true,d.name,v.input))
					command("ROLLBACK TO geqo_failure"); check(d.a,"session"); command(local); check(d.b,"session"); command("COMMIT"); check(d.a,"session")
					for _, reset := range []string{"RESET "+d.name,"SET "+d.name+"=DEFAULT","RESET ALL","SELECT set_config($1,NULL,false)"} {
						command(set); check(d.a,"session")
						if reset == "SELECT set_config($1,NULL,false)" { step := fixture.Select(reset,[]string{"set_config"},[]string{"TEXT"},[][]any{{d.boot}},d.name); step.Prepared = prepared; s.run(step) } else { command(reset) }
						check(d.boot,"default")
					}
				}
				var notices []fixture.Diagnostic
				_, conn, recorder := fixture.NoticeReferencePool(s.t,s.p,s.pg,&notices)
				defer conn.Close()
				observed := &physicalCase{s.t,s.p,conn,s.pg,recorder}
				for _, prepared := range []bool{false,true} {
					notices = nil
					step := fixture.Exec("SET LOCAL "+d.name+"="+physicalLiteral(d.b),0); step.Prepared = prepared; observed.run(step)
					want := []fixture.Diagnostic{{Severity:"WARNING",SeverityUnlocalized:"WARNING",Code:"25P01",Message:"SET LOCAL can only be used in transaction blocks"}}
					if !reflect.DeepEqual(notices,want) { s.t.Fatalf("notices %#v want %#v",notices,want) }
					observed.geqoValue(d,d.boot,"default",prepared)
				}
			})
		})
	}
}
