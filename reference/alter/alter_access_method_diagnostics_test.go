package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterAccessMethodDiagnostics(t *testing.T) {
	t.Run("methods-and-targets",func(t *testing.T) {
		accessMethodScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			for _,q:=range []string{
				"CREATE TABLE t(id int)","CREATE INDEX idx ON t(id)","CREATE VIEW v AS SELECT id FROM t","CREATE SEQUENCE seq",
				"CREATE TYPE pair AS(id int)",
				"CREATE FOREIGN DATA WRAPPER access_method_fdw","CREATE SERVER access_method_server FOREIGN DATA WRAPPER access_method_fdw",
				"CREATE FOREIGN TABLE foreign_t(id int) SERVER access_method_server",
				`CREATE ACCESS METHOD "Heap 方法" TYPE TABLE HANDLER heap_tableam_handler`,
			} {persistenceExec(t,p,b,c,q)}
			defer func(){if _,err:=c.ExecContext(p.Ctx,"DROP FOREIGN DATA WRAPPER access_method_fdw CASCADE");err!=nil {t.Error(err)}}()
			for _,prepared:=range []bool{false,true} {
				persistenceFailure(t,p,b,c,"ALTER TABLE t SET ACCESS METHOD missing_am",prepared,persistenceDiagnostic("42704",`access method "missing_am" does not exist`))
				for _,method:=range []string{"btree","hash","gist","brin","spgist"} {
					// Each installed index method has a wrong-kind table diagnostic.
					persistenceFailure(t,p,b,c,"ALTER TABLE t SET ACCESS METHOD "+method,prepared,persistenceDiagnostic("55000",`access method "`+method+`" is not of type TABLE`))
				}
				// Partitioned-index construction is unavailable publicly; its
				// exact rejection is covered with a real descriptor in owner units.
				for _,target:=range []struct{name,kind string}{{"idx","indexes"},{"v","views"},{"seq","sequences"},{"foreign_t","foreign tables"}} {
					want:=persistenceDiagnostic("42809",`ALTER action SET ACCESS METHOD cannot be performed on relation "`+target.name+`"`)
					want.Detail="This operation is not supported for "+target.kind+"."
					persistenceFailure(t,p,b,c,"ALTER TABLE "+target.name+" SET ACCESS METHOD heap2",prepared,want)
				}
				want:=persistenceDiagnostic("42809",`"pair" is a composite type`);want.Hint="Use ALTER TYPE instead."
				persistenceFailure(t,p,b,c,"ALTER TABLE pair SET ACCESS METHOD heap2",prepared,want)
				persistenceFailure(t,p,b,c,"ALTER TABLE pg_class SET ACCESS METHOD heap2",prepared,persistenceDiagnostic("42501",`permission denied: "pg_class" is a system catalog`))
				persistenceFailure(t,p,b,c,"ALTER MATERIALIZED VIEW t SET ACCESS METHOD heap2",prepared,persistenceDiagnostic("42809",`"t" is not a materialized view`))
				for _,list:=range []string{"heap2, SET ACCESS METHOD heap","heap2, SET ACCESS METHOD heap2","heap2, SET ACCESS METHOD missing_am"} {
					persistenceFailure(t,p,b,c,"ALTER TABLE t SET ACCESS METHOD "+list,prepared,persistenceDiagnostic("0A000","cannot have multiple SET ACCESS METHOD subcommands"))
					accessMethodProperty(t,p,b,c,"t","heap")
				}
				for _,list:=range []string{"heap, SET ACCESS METHOD heap","heap, SET ACCESS METHOD heap2"} {
					step:=fixture.Exec("ALTER TABLE t SET ACCESS METHOD "+list,0);if prepared {step=fixture.Prepared(step)};persistenceRun(t,p,b,c,step)
				}
				accessMethodProperty(t,p,b,c,"t","heap2")
				persistenceExec(t,p,b,c,"ALTER TABLE t SET ACCESS METHOD heap")
				step:=fixture.Exec(`ALTER TABLE t SET ACCESS METHOD "Heap 方法"`,0);if prepared {step=fixture.Prepared(step)};persistenceRun(t,p,b,c,step)
				accessMethodProperty(t,p,b,c,"t","Heap 方法")
				persistenceExec(t,p,b,c,"ALTER TABLE t SET ACCESS METHOD heap")
				syntax:=persistenceDiagnostic("42601",`syntax error at or near "trailing"`)
				syntax.Position=fixture.Position(len("ALTER TABLE t SET ACCESS METHOD heap2 ")+1)
				malformed:=fixture.ErrorDiagnostic("ALTER TABLE t SET ACCESS METHOD heap2 trailing",syntax)
				malformed.ErrorStage="execute"
				if prepared {malformed=fixture.Prepared(malformed);malformed.ErrorStage="prepare"}
				persistenceRun(t,p,b,c,malformed)
				accessMethodProperty(t,p,b,c,"t","heap")
			}
		})
	})
	t.Run("owner-precedence",func(t *testing.T) {
		fixture.RoleReferenceCleanup(t,[]string{"am_owner","am_guest"})
		accessMethodScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			for _,q:=range []string{"CREATE ROLE am_owner","CREATE ROLE am_guest","GRANT USAGE ON SCHEMA public TO am_owner,am_guest","CREATE TABLE t(id int)","ALTER TABLE t OWNER TO am_owner","GRANT ALL ON t TO am_guest","SET ROLE am_guest"} {persistenceExec(t,p,b,c,q)}
			defer c.ExecContext(p.Ctx,"RESET ROLE")
			for _,prepared:=range []bool{false,true} {for _,method:=range []string{"heap","heap2","missing_am","btree"} {
				persistenceFailure(t,p,b,c,"ALTER TABLE t SET ACCESS METHOD "+method,prepared,persistenceDiagnostic("42501","must be owner of table t"))
			}}
			persistenceExec(t,p,b,c,"RESET ROLE")
			accessMethodProperty(t,p,b,c,"t","heap")
		})
	})
	t.Run("conditional-notice",func(t *testing.T) {
		p:=fixture.NewScenario(t)
		for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
			var notices []fixture.Diagnostic
			_,c,rec:=fixture.NoticeReferencePool(t,p,b.PG,&notices);b.Recorder=rec
			for _,prepared:=range []bool{false,true} {
				notices=nil
				step:=fixture.Exec("ALTER TABLE IF EXISTS absent_am SET ACCESS METHOD missing_am",0);if prepared {step=fixture.Prepared(step)};persistenceRun(t,p,b,c,step)
				want:=fixture.Diagnostic{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:`relation "absent_am" does not exist, skipping`}
				if len(notices)!=1 || notices[0]!=want {t.Fatal(notices,want)}
			}
		})}
	})
}
