package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreRowsPrivileges(t *testing.T) {
	for _,name:=range []string{"skeys","svals","each"} {
		t.Run(name,func(t *testing.T){
			fixture.RoleReferenceCleanup(t,[]string{"hs_rows_actor"})
			fixture.SchemaReferenceCleanup(t,"hs_rows_schema")
			hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
				hstoreInstall(t,p,b,c)
				for _,q:=range []string{"CREATE ROLE hs_rows_actor","GRANT USAGE ON SCHEMA public TO hs_rows_actor","CREATE TABLE hs_rows_acl(id integer,v hstore)","GRANT SELECT ON hs_rows_acl TO hs_rows_actor","CREATE SCHEMA hs_rows_schema","GRANT USAGE ON SCHEMA hs_rows_schema TO hs_rows_actor"}{hstoreRun(t,p,b,c,fixture.Exec(q,0))}
				hstoreRun(t,p,b,c,fixture.Exec("INSERT INTO hs_rows_acl VALUES (1,'a=>x'),(2,'a=>NULL'),(3,NULL)",3))
				// Keep both non-NULL and whole-NULL demands prepared as owner, then
				// execute as the actor. Strictness must not bypass caller admission.
				for _,id:=range []string{"1","2","3"} {
					q:="SELECT "+name+"(v) FROM hs_rows_acl WHERE id="+id
					stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
					func(){
						defer stmt.Close()
						held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
						hstoreRun(t,p,b,c,fixture.Exec("REVOKE EXECUTE ON FUNCTION "+name+"(hstore) FROM PUBLIC,hs_rows_actor",0))
						hstoreRun(t,p,b,c,fixture.Exec("SET ROLE hs_rows_actor",0))
						want:=fixture.ControlsReferenceDiagnostic("42501","permission denied for function "+name)
						for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,q,prepared,true,false,want)}
						hstoreError(t,p,b,held,q,false,true,false,want)
						// SRFs are not scalar-folded strict calls. Independently predict
						// permission checking even for literal NULL; root confirms 18.6.
						for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,"SELECT * FROM "+name+"(NULL::hstore)",prepared,true,false,want)}
						hstoreRun(t,p,b,c,fixture.Exec("RESET ROLE",0))
						hstoreRun(t,p,b,c,fixture.Exec("GRANT EXECUTE ON FUNCTION "+name+"(hstore) TO hs_rows_actor",0))
						hstoreRun(t,p,b,c,fixture.Exec("SET ROLE hs_rows_actor",0))
						var values [][]any
						if id!="3" {value:=any("x");if id=="2"{value=nil};if name=="skeys"{value="a"};if name=="each"{value="(a,x)";if id=="2"{value="(a,)"}};values=[][]any{{value}}}
						s:=fixture.Select(q,[]string{name},[]string{"TEXT"},values);s.Bag=true
						if name=="each" {s.Types=[]string{"RECORD"}} else {s=hstoreRowsSelect(q,[]string{name},values)}
						hstoreRowsBoth(t,p,b,c,s);hstoreRun(t,p,b,held,s)
						hstoreRun(t,p,b,c,fixture.Exec("RESET ROLE",0))
					}()
				}
				hstoreRun(t,p,b,c,fixture.Exec("ALTER FUNCTION "+name+"(hstore) SET SCHEMA hs_rows_schema",0))
				q:="SELECT * FROM hs_rows_schema."+name+"((SELECT v FROM hs_rows_acl WHERE id=2))"
				stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
				held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
				values:=[][]any{{nil}};if name=="skeys"{values=[][]any{{"a"}}};if name=="each"{values=[][]any{{"a",nil}}}
				s:=hstoreRowsSelect(q,hstoreRowsNames(name),values)
				hstoreRun(t,p,b,c,fixture.Exec("SET ROLE hs_rows_actor",0));hstoreRun(t,p,b,held,s)
				hstoreRun(t,p,b,c,fixture.Exec("RESET ROLE",0));hstoreRun(t,p,b,c,fixture.Exec("REVOKE USAGE ON SCHEMA hs_rows_schema FROM hs_rows_actor",0));hstoreRun(t,p,b,c,fixture.Exec("SET ROLE hs_rows_actor",0))
				want:=fixture.ControlsReferenceDiagnostic("42501","permission denied for schema hs_rows_schema");want.Position=strconv.Itoa(strings.Index(q,"hs_rows_schema")+1)
				for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,q,prepared,true,true,want)}
				hstoreError(t,p,b,held,q,false,true,false,want)
				hstoreRun(t,p,b,c,fixture.Exec("RESET ROLE",0));hstoreRun(t,p,b,c,fixture.Exec("GRANT USAGE ON SCHEMA hs_rows_schema TO hs_rows_actor",0));hstoreRun(t,p,b,c,fixture.Exec("SET ROLE hs_rows_actor",0))
				hstoreRowsBoth(t,p,b,c,s);hstoreRun(t,p,b,held,s);hstoreRun(t,p,b,c,fixture.Exec("RESET ROLE",0))
			})
		})
	}
}
