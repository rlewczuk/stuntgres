package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func persistenceTargetCases(t *testing.T) {
	for _,prepared:=range []bool{false,true} {
		t.Run(map[bool]string{false:"direct",true:"prepared"}[prepared],func(t *testing.T) {
			persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				for _,q:=range []string{"CREATE TABLE t(id int)","CREATE INDEX idx ON t(id)","CREATE VIEW v AS SELECT id FROM t","CREATE MATERIALIZED VIEW mv AS SELECT id FROM t","CREATE TYPE pair AS(id int)","CREATE SEQUENCE seq CACHE 5"} {persistenceExec(t,p,b,c,q)}
				persistenceExec(t,p,b,c,"CREATE FOREIGN DATA WRAPPER persistence_fdw")
				defer func() { if _,err:=c.ExecContext(p.Ctx,"DROP FOREIGN DATA WRAPPER persistence_fdw CASCADE");err!=nil {t.Error(err)} }()
				persistenceExec(t,p,b,c,"CREATE SERVER persistence_server FOREIGN DATA WRAPPER persistence_fdw")
				persistenceExec(t,p,b,c,"CREATE FOREIGN TABLE foreign_t(id int) SERVER persistence_server")
				for _,target:=range []struct{name,kind string}{{"idx","indexes"},{"v","views"},{"mv","materialized views"},{"foreign_t","foreign tables"}} {
					for _,mode:=range []string{"LOGGED","UNLOGGED"} {
						want:=persistenceDiagnostic("42809",`ALTER action SET `+mode+` cannot be performed on relation "`+target.name+`"`)
						want.Detail="This operation is not supported for "+target.kind+"."
						persistenceFailure(t,p,b,c,"ALTER TABLE "+target.name+" SET "+mode,prepared,want)
					}
				}
				want:=persistenceDiagnostic("42809",`"pair" is a composite type`);want.Hint="Use ALTER TYPE instead."
				persistenceFailure(t,p,b,c,"ALTER TABLE pair SET LOGGED",prepared,want)
				persistenceValue(t,p,b,c,"nextval('seq')",1)
				step:=fixture.Exec("ALTER TABLE seq SET LOGGED, SET UNLOGGED",0);if prepared {step=fixture.Prepared(step)};persistenceRun(t,p,b,c,step)
				persistenceProperty(t,p,b,c,"seq","u")
				persistenceValue(t,p,b,c,"nextval('seq')",6)
				persistenceExec(t,p,b,c,"ALTER TABLE seq SET UNLOGGED")
				persistenceValue(t,p,b,c,"nextval('seq')",7)
				persistenceFailure(t,p,b,c,"ALTER TABLE seq SET LOGGED, SET UNLOGGED",prepared,persistenceDiagnostic("0A000","cannot change persistence setting twice"))
				persistenceProperty(t,p,b,c,"seq","u")
				want=persistenceDiagnostic("42809",`ALTER action SET TABLESPACE cannot be performed on relation "seq"`);want.Detail="This operation is not supported for sequences."
				for _,list:=range []string{"SET LOGGED, SET TABLESPACE pg_default","SET TABLESPACE pg_default, SET LOGGED"} {persistenceFailure(t,p,b,c,"ALTER TABLE seq "+list,prepared,want);persistenceProperty(t,p,b,c,"seq","u")}
				persistenceValue(t,p,b,c,"nextval('seq')",8)
				want=persistenceDiagnostic("42809",`ALTER action ADD COLUMN cannot be performed on relation "seq"`);want.Detail="This operation is not supported for sequences."
				for _,list:=range []string{"SET LOGGED, ADD COLUMN impossible int","ADD COLUMN impossible int, SET LOGGED","SET UNLOGGED, ADD COLUMN impossible int"} {persistenceFailure(t,p,b,c,"ALTER TABLE seq "+list,prepared,want);persistenceProperty(t,p,b,c,"seq","u")}
				persistenceValue(t,p,b,c,"nextval('seq')",9)
				persistenceExec(t,p,b,c,"ALTER TABLE seq SET UNLOGGED, SET LOGGED")
				persistenceProperty(t,p,b,c,"seq","p")
				persistenceFailure(t,p,b,c,"ALTER TABLE seq SET UNLOGGED, SET UNLOGGED",prepared,persistenceDiagnostic("0A000","cannot change persistence setting twice"))
				persistenceProperty(t,p,b,c,"seq","p")
				persistenceValue(t,p,b,c,"nextval('seq')",11)
				persistenceExec(t,p,b,c,"CREATE TEMP SEQUENCE temp_seq")
				var ns string
				if err:=c.QueryRowContext(p.Ctx,"SELECT n.nspname FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE c.oid='temp_seq'::regclass").Scan(&ns);err!=nil {t.Fatal(err)}
				want=persistenceDiagnostic("42P16",`cannot change logged status of table "temp_seq" because it is temporary`);want.Schema,want.Table=ns,"temp_seq"
				for _,mode:=range []string{"LOGGED","UNLOGGED"} {persistenceFailure(t,p,b,c,"ALTER TABLE temp_seq SET "+mode,prepared,want)}
			})
		})
	}
}
