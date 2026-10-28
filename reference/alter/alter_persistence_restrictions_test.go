package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterPersistenceRestrictions(t *testing.T) {
	t.Run("targets",persistenceTargetCases)
	for _, prepared := range []bool{false,true} {
		t.Run(map[bool]string{false:"direct",true:"prepared"}[prepared],func(t *testing.T) {
			persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				persistenceFailure(t,p,b,c,"CREATE UNLOGGED TABLE t(id int) PARTITION BY RANGE(id)",prepared,persistenceDiagnostic("0A000","partitioned tables cannot be unlogged"))
				persistenceExec(t,p,b,c,"CREATE TABLE parts(id int) PARTITION BY RANGE(id)")
				persistenceExec(t,p,b,c,"CREATE TEMP TABLE temp_t(id int)")
				var ns string
				if err := c.QueryRowContext(p.Ctx,"SELECT n.nspname FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE c.oid='temp_t'::regclass").Scan(&ns); err != nil { t.Fatal(err) }
				for _, mode := range []string{"LOGGED","UNLOGGED"} {
					want := persistenceDiagnostic("42P16",`cannot change logged status of table "temp_t" because it is temporary`)
					want.Schema, want.Table = ns,"temp_t"
					persistenceFailure(t,p,b,c,"ALTER TABLE temp_t SET "+mode,prepared,want)
					want = persistenceDiagnostic("42809",`ALTER action SET `+mode+` cannot be performed on relation "parts"`)
					want.Detail="This operation is not supported for partitioned tables."
					persistenceFailure(t,p,b,c,"ALTER TABLE parts SET "+mode,prepared,want)
				}
				want:=persistenceDiagnostic("42P16","only temporary relations may be created in temporary schemas")
				want.Position="23"
				persistenceFailure(t,p,b,c,"CREATE UNLOGGED TABLE pg_temp.wrong(id int)",prepared,want)
				persistenceExec(t,p,b,c,"CREATE TABLE root(id int)")
				persistenceExec(t,p,b,c,"CREATE TABLE child() INHERITS(root)")
				persistenceExec(t,p,b,c,"ALTER TABLE root SET UNLOGGED")
				persistenceProperty(t,p,b,c,"root","u")
				persistenceProperty(t,p,b,c,"child","p")
				persistenceExec(t,p,b,c,"ALTER TABLE root SET LOGGED, ADD COLUMN note text")
				persistenceProperty(t,p,b,c,"root","p")
				persistenceProperty(t,p,b,c,"child","p")
			})
		})
	}
	t.Run("owner",func(t *testing.T) {
		fixture.RoleReferenceCleanup(t,[]string{"persist_owner","persist_guest"})
		persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			for _, q := range []string{"CREATE ROLE persist_owner","CREATE ROLE persist_guest","GRANT USAGE ON SCHEMA public TO persist_owner,persist_guest","CREATE TABLE t(id int)","ALTER TABLE t OWNER TO persist_owner","GRANT ALL ON t TO persist_guest","CREATE SEQUENCE owner_seq CACHE 5","ALTER SEQUENCE owner_seq OWNER TO persist_owner","GRANT ALL ON SEQUENCE owner_seq TO persist_guest","CREATE TYPE owner_pair AS(id int)","ALTER TYPE owner_pair OWNER TO persist_owner","GRANT USAGE ON TYPE owner_pair TO persist_guest","SET ROLE persist_guest"} { persistenceExec(t,p,b,c,q) }
			defer c.ExecContext(p.Ctx,"RESET ROLE")
			for _, prepared := range []bool{false,true} { persistenceFailure(t,p,b,c,"ALTER TABLE t SET UNLOGGED",prepared,persistenceDiagnostic("42501","must be owner of table t")) }
			for _,prepared:=range []bool{false,true} {
				for _,mode:=range []string{"LOGGED","UNLOGGED"} {
					persistenceFailure(t,p,b,c,"ALTER TABLE owner_seq SET "+mode,prepared,persistenceDiagnostic("42501","must be owner of sequence owner_seq"))
					persistenceFailure(t,p,b,c,"ALTER TABLE owner_pair SET "+mode,prepared,persistenceDiagnostic("42501","must be owner of table owner_pair"))
				}
			}
			persistenceProperty(t,p,b,c,"owner_seq","p")
			persistenceProperty(t,p,b,c,"owner_pair","p")
			persistenceRun(t,p,b,c,fixture.Select("SELECT last_value,is_called FROM owner_seq",[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(1),false}}))
			persistenceExec(t,p,b,c,"SET ROLE persist_owner")
			persistenceExec(t,p,b,c,"ALTER TABLE t SET UNLOGGED")
			persistenceProperty(t,p,b,c,"t","u")
			persistenceExec(t,p,b,c,"ALTER TABLE owner_seq SET UNLOGGED")
			persistenceProperty(t,p,b,c,"owner_seq","u")
			persistenceExec(t,p,b,c,"RESET ROLE")
			persistenceExec(t,p,b,c,"ALTER TABLE t SET LOGGED")
			persistenceExec(t,p,b,c,"ALTER TABLE owner_seq SET LOGGED")
			persistenceProperty(t,p,b,c,"owner_seq","p")
		})
	})
}

func TestReferenceAlterPersistenceForeignKeys(t *testing.T) {
	for _, child := range []string{"","UNLOGGED ","TEMP "} {
		for _, parent := range []string{"","UNLOGGED ","TEMP "} {
			t.Run(child+"to"+parent,func(t *testing.T) {
				persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
					persistenceExec(t,p,b,c,"CREATE "+parent+"TABLE parent(id int PRIMARY KEY)")
					q := "CREATE "+child+"TABLE child(id int REFERENCES parent)"
					message := ""
					if child=="" && parent!="" { message="constraints on permanent tables may reference only permanent tables" }
					if child=="UNLOGGED " && parent=="TEMP " { message="constraints on unlogged tables may reference only permanent or unlogged tables" }
					if child=="TEMP " && parent!="TEMP " { message="constraints on temporary tables may reference only temporary tables" }
					if message!="" { persistenceFailure(t,p,b,c,q,false,persistenceDiagnostic("42P16",message)) } else { persistenceExec(t,p,b,c,q) }
				})
			})
		}
	}
	for _, suffix := range []string{""," NOT VALID"," NOT ENFORCED"} {
		t.Run("existing"+suffix,func(t *testing.T) {
			persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				persistenceExec(t,p,b,c,"CREATE UNLOGGED TABLE parent(id int PRIMARY KEY)")
				persistenceExec(t,p,b,c,"CREATE UNLOGGED TABLE child(id int)")
				persistenceExec(t,p,b,c,"ALTER TABLE child ADD CONSTRAINT child_fk FOREIGN KEY(id) REFERENCES parent"+suffix)
				want := persistenceDiagnostic("42P16",`could not change table "child" to logged because it references unlogged table "parent"`)
				want.Schema,want.Table,want.Constraint="public","child","child_fk"
				persistenceFailure(t,p,b,c,"ALTER TABLE child SET LOGGED",false,want)
				persistenceExec(t,p,b,c,"ALTER TABLE child DROP CONSTRAINT child_fk")
				persistenceExec(t,p,b,c,"ALTER TABLE parent SET LOGGED")
				persistenceExec(t,p,b,c,"ALTER TABLE child SET LOGGED")
				persistenceExec(t,p,b,c,"ALTER TABLE child ADD CONSTRAINT child_fk FOREIGN KEY(id) REFERENCES parent"+suffix)
				want=persistenceDiagnostic("42P16",`could not change table "parent" to unlogged because it references logged table "child"`)
				want.Schema,want.Table,want.Constraint="public","parent","child_fk"
				persistenceFailure(t,p,b,c,"ALTER TABLE parent SET UNLOGGED",true,want)
			})
		})
	}
	persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		persistenceExec(t,p,b,c,"CREATE TABLE self(id int PRIMARY KEY,parent int REFERENCES self)")
		for _, mode := range []string{"UNLOGGED","LOGGED"} { persistenceExec(t,p,b,c,"ALTER TABLE self SET "+mode) }
	})
}

func TestReferenceAlterPersistencePreparation(t *testing.T) {
	for _, prepared := range []bool{false,true} {
		for _, reverse := range []bool{false,true} {
			for _, logged := range []bool{false,true} {
				t.Run(map[bool]string{false:"direct",true:"prepared"}[prepared]+map[bool]string{false:"forward",true:"reverse"}[reverse]+map[bool]string{false:"u",true:"p"}[logged],func(t *testing.T) {
					persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
						persistenceExec(t,p,b,c,"CREATE UNLOGGED TABLE parent(id int PRIMARY KEY)")
						prefix,mode,property := "UNLOGGED ","LOGGED","p"
						if logged { prefix,mode,property="","UNLOGGED","p" }
						persistenceExec(t,p,b,c,"CREATE "+prefix+"TABLE child(id int)")
						add := "ADD CONSTRAINT child_fk FOREIGN KEY(id) REFERENCES parent"
						set := "SET "+mode
						list:=set+", "+add; if reverse { list=add+", "+set }
						q:="ALTER TABLE child "+list
						if logged { persistenceFailure(t,p,b,c,q,prepared,persistenceDiagnostic("42P16","constraints on permanent tables may reference only permanent tables")) } else {
							step:=fixture.Exec(q,0); if prepared { step=fixture.Prepared(step) }; persistenceRun(t,p,b,c,step)
							persistenceExec(t,p,b,c,"ALTER TABLE child SET LOGGED")
							persistenceExec(t,p,b,c,"ALTER TABLE child SET UNLOGGED")
							want:=persistenceDiagnostic("42P16",`could not change table "child" to logged because it references unlogged table "parent"`)
							want.Schema,want.Table,want.Constraint="public","child","child_fk"
							list="DROP CONSTRAINT child_fk, SET LOGGED"; if reverse { list="SET LOGGED, DROP CONSTRAINT child_fk" }
							persistenceFailure(t,p,b,c,"ALTER TABLE child "+list,prepared,want)
							property="u"
						}
						persistenceProperty(t,p,b,c,"child",property)
					})
				})
			}
		}
	}
	for _, initial := range []string{"LOGGED","UNLOGGED"} {
		for _, list := range []string{"same,same","same,other","other,same","other,other"} {
			t.Run(initial+list,func(t *testing.T) {
				persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
					prefix,other,property:="","UNLOGGED","p"; if initial=="UNLOGGED" {prefix,other,property="UNLOGGED ","LOGGED","u"}
					persistenceExec(t,p,b,c,"CREATE "+prefix+"TABLE t(id int)")
					actions:=map[string]string{"same,same":"SET "+initial+", SET "+initial,"same,other":"SET "+initial+", SET "+other,"other,same":"SET "+other+", SET "+initial,"other,other":"SET "+other+", SET "+other}
					q:="ALTER TABLE t "+actions[list]
					if list=="other,same" || list=="other,other" { persistenceFailure(t,p,b,c,q,true,persistenceDiagnostic("0A000","cannot change persistence setting twice")) } else { persistenceExec(t,p,b,c,q); if list=="same,other" {if property=="p" {property="u"} else {property="p"}} }
					persistenceProperty(t,p,b,c,"t",property)
				})
			})
		}
	}
}
