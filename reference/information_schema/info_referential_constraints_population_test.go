package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoReferentialConstraintsPopulation(t *testing.T) {
	ircScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,db string) {
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,r,s)}
		ircSeed(run)
		want:=[][]any{ircRow(db,"irc_scope","child_fk","irc_scope","parent_pk","NONE","NO ACTION","NO ACTION")}
		actions:=[]string{"NO ACTION","RESTRICT","CASCADE","SET NULL","SET DEFAULT"}
		for i,update:=range actions {
			name:=fmt.Sprintf("action_%d",i); deleteRule:=actions[(i+1)%len(actions)]
			run(fixture.Exec("CREATE TABLE irc_scope."+name+"(pid int,CONSTRAINT "+name+" FOREIGN KEY(pid) REFERENCES irc_scope.parent ON UPDATE "+update+" ON DELETE "+deleteRule+")",0))
			want=append(want,ircRow(db,"irc_scope",name,"irc_scope","parent_pk","NONE",update,deleteRule))
		}
		for _,q:=range []string{
			"CREATE TABLE irc_scope.composite(a int,b int,CONSTRAINT composite_u UNIQUE(a,b))",
			"CREATE TABLE irc_scope.full_child(a int,b int,CONSTRAINT full_fk FOREIGN KEY(a,b) REFERENCES irc_scope.composite(a,b) MATCH FULL)",
			"CREATE TABLE irc_scope.reordered(a int,b int,CONSTRAINT reordered_fk FOREIGN KEY(a,b) REFERENCES irc_scope.composite(b,a))",
			"CREATE TABLE irc_scope.self_ref(id int CONSTRAINT self_pk PRIMARY KEY,pid int,CONSTRAINT self_fk FOREIGN KEY(pid) REFERENCES irc_scope.self_ref)",
			"CREATE TABLE irc_scope.not_valid(pid int)",
			"ALTER TABLE irc_scope.not_valid ADD CONSTRAINT nv_fk FOREIGN KEY(pid) REFERENCES irc_scope.parent NOT VALID",
			"CREATE TABLE irc_scope.index_parent(id int)",
			"CREATE UNIQUE INDEX standalone ON irc_scope.index_parent(id)",
			"CREATE TABLE irc_scope.index_child(pid int,CONSTRAINT index_fk FOREIGN KEY(pid) REFERENCES irc_scope.index_parent(id))",
			"ALTER TABLE irc_scope.index_parent ADD CONSTRAINT later_unique UNIQUE(id)",
			"CREATE TABLE irc_scope.duplicate_a(pid int,CONSTRAINT same FOREIGN KEY(pid) REFERENCES irc_scope.parent)",
			"CREATE TABLE irc_scope.duplicate_b(pid int,CONSTRAINT same FOREIGN KEY(pid) REFERENCES irc_scope.parent)",
			"CREATE SCHEMA irc_other",
			"CREATE TABLE irc_scope.named(id int CONSTRAINT target_same UNIQUE)",
			"CREATE TABLE irc_other.named(id int CONSTRAINT target_same UNIQUE)",
			"CREATE TABLE irc_scope.named_child(a int,b int,CONSTRAINT first_fk FOREIGN KEY(a) REFERENCES irc_scope.named(id),CONSTRAINT second_fk FOREIGN KEY(b) REFERENCES irc_other.named(id))",
			"CREATE TABLE irc_scope.competitor(id int CONSTRAINT original_unique UNIQUE)",
			"CREATE TABLE irc_scope.competitor_child(pid int,CONSTRAINT competitor_fk FOREIGN KEY(pid) REFERENCES irc_scope.competitor(id))",
			"ALTER TABLE irc_scope.competitor ADD CONSTRAINT later_competitor UNIQUE(id)",
			"ALTER TABLE irc_scope.competitor RENAME CONSTRAINT original_unique TO renamed_unique",
			"CREATE TABLE irc_scope.check_only(id int CHECK(id>0))",
		} {run(fixture.Exec(q,0))}
		want=append(want,
			ircRow(db,"irc_scope","full_fk","irc_scope","composite_u","FULL","NO ACTION","NO ACTION"),
			ircRow(db,"irc_scope","reordered_fk","irc_scope","composite_u","NONE","NO ACTION","NO ACTION"),
			ircRow(db,"irc_scope","self_fk","irc_scope","self_pk","NONE","NO ACTION","NO ACTION"),
			ircRow(db,"irc_scope","nv_fk","irc_scope","parent_pk","NONE","NO ACTION","NO ACTION"),
			ircNullRow(db,"irc_scope","index_fk"),
			ircRow(db,"irc_scope","same","irc_scope","parent_pk","NONE","NO ACTION","NO ACTION"),
			ircRow(db,"irc_scope","same","irc_scope","parent_pk","NONE","NO ACTION","NO ACTION"),
			ircRow(db,"irc_scope","first_fk","irc_scope","target_same","NONE","NO ACTION","NO ACTION"),
			ircRow(db,"irc_scope","second_fk","irc_other","target_same","NONE","NO ACTION","NO ACTION"),
			ircRow(db,"irc_scope","competitor_fk","irc_scope","renamed_unique","NONE","NO ACTION","NO ACTION"))
		s:=ircProjection(b.PG,"constraint_schema='irc_scope'",want)
		ircCheck(t,p,c,r,ircHold(t,p,c,s.Query),s)
		run(fixture.Select(`SELECT f.conindid=u.conindid AS same_index,f.confrelid=u.conrelid AS same_target FROM pg_catalog.pg_constraint f JOIN pg_catalog.pg_constraint u ON u.conname='renamed_unique' AND u.conrelid='irc_scope.competitor'::regclass WHERE f.conrelid='irc_scope.competitor_child'::regclass AND f.conname='competitor_fk'`,[]string{"same_index","same_target"},[]string{"BOOL","BOOL"},[][]any{{true,true}}))
		run(fixture.Select(`SELECT f.conindid=i.indexrelid AS standalone_index FROM pg_catalog.pg_constraint f JOIN pg_catalog.pg_index i ON i.indexrelid='irc_scope.standalone'::regclass WHERE f.conrelid='irc_scope.index_child'::regclass AND f.conname='index_fk'`,[]string{"standalone_index"},[]string{"BOOL"},[][]any{{true}}))
	})
}
