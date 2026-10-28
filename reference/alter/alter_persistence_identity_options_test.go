package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func persistenceIdentityOptionCases(t *testing.T) {
	for _,prepared:=range []bool{false,true} {
		for _,add:=range []bool{false,true} {
			for _,tableMode:=range []string{"LOGGED","UNLOGGED"} {
				for _,option:=range []string{"","LOGGED","UNLOGGED"} {
					name:=map[bool]string{false:"direct",true:"prepared"}[prepared]+"/"+map[bool]string{false:"create",true:"add"}[add]+"/"+tableMode+"/"+option
					t.Run(name,func(t *testing.T) {
						persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
							prefix,tableProperty:="","p";if tableMode=="UNLOGGED" {prefix,tableProperty="UNLOGGED ","u"}
							identity:="GENERATED ALWAYS AS IDENTITY";if option!="" {identity+=" ("+option+")"}
							query:="CREATE "+prefix+"TABLE identity_choice(id int "+identity+")"
							if add {persistenceExec(t,p,b,c,"CREATE "+prefix+"TABLE identity_choice(id int NOT NULL)");query="ALTER TABLE identity_choice ALTER id ADD "+identity}
							step:=fixture.Exec(query,0);if prepared {step=fixture.Prepared(step)};persistenceRun(t,p,b,c,step)
							sequenceProperty:=tableProperty;if option=="LOGGED" {sequenceProperty="p"};if option=="UNLOGGED" {sequenceProperty="u"}
							persistenceProperty(t,p,b,c,"identity_choice",tableProperty)
							persistenceProperty(t,p,b,c,"identity_choice_id_seq",sequenceProperty)
							persistenceExec(t,p,b,c,"ALTER TABLE identity_choice SET "+tableMode)
							persistenceProperty(t,p,b,c,"identity_choice_id_seq",sequenceProperty)
							persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO identity_choice DEFAULT VALUES",1))
							rows:=fixture.Select("SELECT id FROM identity_choice",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)}})
							persistenceRun(t,p,b,c,rows)
							if !b.PG {
								var dump bytes.Buffer
								if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
								connector,err:=stuntgres.NewDriver().OpenConnector("identity-declaration");if err!=nil {t.Fatal(err)}
								target:=sql.OpenDB(connector);defer target.Close()
								for range 2 {
									if err=stuntgres.Restore(p.Ctx,target,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)}
									persistenceControlRun(t,p,target,persistencePropertyStep("identity_choice",tableProperty))
									persistenceControlRun(t,p,target,persistencePropertyStep("identity_choice_id_seq",sequenceProperty))
									persistenceControlRun(t,p,target,rows)
									var again bytes.Buffer
									if err=stuntgres.Dump(p.Ctx,target,&again);err!=nil || !bytes.Equal(dump.Bytes(),again.Bytes()) {t.Fatal("identity declaration redump",err)}
								}
								persistenceControlValue(t,p,target,"nextval('identity_choice_id_seq')",2)
							}
						})
					})
				}
			}
		}
	}
}
