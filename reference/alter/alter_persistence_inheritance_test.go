package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func persistenceInheritanceCases(t *testing.T) {
	for _,prepared:=range []bool{false,true} {
		for _,reverse:=range []bool{false,true} {
			for _,unlogged:=range []bool{false,true} {
				name:=map[bool]string{false:"direct",true:"prepared"}[prepared]+map[bool]string{false:"forward",true:"reverse"}[reverse]+map[bool]string{false:"p",true:"u"}[unlogged]
				t.Run(name,func(t *testing.T) {
					persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
						prefix,mode,old,new:="","UNLOGGED","p","u";if unlogged {prefix,mode,old,new="UNLOGGED ","LOGGED","u","p"}
						persistenceExec(t,p,b,c,"CREATE "+prefix+"TABLE root(id int)")
						persistenceExec(t,p,b,c,"CREATE TABLE child() INHERITS(root)")
						persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO root VALUES(7)",1))
						persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO child VALUES(8)",1))
						list:="SET "+mode+", ADD COLUMN added int DEFAULT 9";if reverse {list="ADD COLUMN added int DEFAULT 9, SET "+mode}
						want:=persistenceDiagnostic("23514",`check constraint "bad" of relation "root" is violated by some row`);want.Schema,want.Table,want.Constraint="public","root","bad"
						persistenceFailure(t,p,b,c,"ALTER TABLE root "+list+", ADD CONSTRAINT bad CHECK(id<0)",prepared,want)
						persistenceProperty(t,p,b,c,"root",old);persistenceProperty(t,p,b,c,"child","p")
						persistenceRun(t,p,b,c,fixture.Select("SELECT attrelid::regclass::text AS name,count(*) FROM pg_attribute WHERE attrelid IN('root'::regclass,'child'::regclass) AND attname='added' GROUP BY attrelid ORDER BY name",[]string{"name","count"},[]string{"TEXT","INT8"},nil))
						step:=fixture.Exec("ALTER TABLE root "+list,0);if prepared {step=fixture.Prepared(step)};persistenceRun(t,p,b,c,step)
						persistenceProperty(t,p,b,c,"root",new);persistenceProperty(t,p,b,c,"child","p")
						for _,table:=range []struct{name string;id int64}{{"root",7},{"child",8}} {persistenceRun(t,p,b,c,fixture.Select("SELECT id,added FROM ONLY "+table.name,[]string{"id","added"},[]string{"INT4","INT4"},[][]any{{table.id,int64(9)}}))}
					})
				})
			}
		}
	}
}
