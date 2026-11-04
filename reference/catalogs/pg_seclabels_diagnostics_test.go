package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// rewriteHandler.c view_query_is_auto_updatable/rewriteTargetView, REL_18_6.
func pgSeclabelsWriteDiagnostic(operation string) fixture.Diagnostic {
	message,action:="cannot update view \"pg_seclabels\"","updating the view"
	if operation=="insert"{message,action="cannot insert into view \"pg_seclabels\"","inserting into the view"}
	if operation=="delete"{message,action="cannot delete from view \"pg_seclabels\"","deleting from the view"}
	d:=pgSeclabelDiagnostic("55000",message)
	d.Detail="Views containing UNION, INTERSECT, or EXCEPT are not automatically updatable."
	d.Hint="To enable "+action+", provide an INSTEAD OF "+strings.ToUpper(operation)+" trigger or an unconditional ON "+strings.ToUpper(operation)+" DO INSTEAD rule."
	return d
}

var pgSeclabelsWrites=[]struct{operation,sql string}{
	{"insert","INSERT INTO pg_catalog.pg_seclabels(provider,label) VALUES('missing','x')"},
	{"insert","INSERT INTO pg_catalog.pg_seclabels(provider,label) SELECT 'missing','x' WHERE FALSE"},
	{"update","UPDATE pg_catalog.pg_seclabels SET label='x' WHERE FALSE"},
	{"delete","DELETE FROM pg_catalog.pg_seclabels WHERE FALSE"},
}

func TestReferencePGSeclabelsDiagnostics(t *testing.T) {
	pgSeclabelsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		want:=pgSeclabelDiagnostic("42703",`column "oid" does not exist`);want.Position="8"
		for _,prepared:=range []bool{false,true}{
			s:=fixture.ErrorDiagnostic("SELECT oid FROM pg_catalog.pg_seclabels",want);s.SelectRows,s.Prepared,s.ErrorStage=true,prepared,"execute";if prepared{s.ErrorStage="prepare"};fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
		}
		for _,write:=range pgSeclabelsWrites {
			for _,prepared:=range []bool{false,true}{
				s:=fixture.ErrorDiagnostic(write.sql,pgSeclabelsWriteDiagnostic(write.operation))
				s.Prepared,s.ErrorStage=prepared,"execute";if prepared{s.ErrorStage="prepare"}
				fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
				pgSeclabelsEmpty(t,p,b,c)
			}
		}
	})
}

func TestReferencePGSeclabelsTransactions(t *testing.T) {
	pgSeclabelsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		pgSeclabelsSetup(t,p,b,c)
		cases:=[]struct{sql string;diagnostic fixture.Diagnostic;prepareError bool}{
			{"SECURITY LABEL ON TABLE sls_private.sentinel IS 'first'",pgSeclabelAbsent(),false},
			{"SECURITY LABEL FOR missing ON COLUMN sls_private.sentinel.note IS NULL",pgSeclabelProvider("missing"),false},
		}
		for _,write:=range pgSeclabelsWrites {cases=append(cases,struct{sql string;diagnostic fixture.Diagnostic;prepareError bool}{write.sql,pgSeclabelsWriteDiagnostic(write.operation),true})}
		for _,tc:=range cases {
			var held fixture.Executor
			// Utilities resolve providers on execution; view writes fail in PrepareContext.
			if !tc.prepareError {held=pgSeclabelHeld(t,p,c,tc.sql)}
			for _,prepared:=range []bool{false,true}{
				x:=fixture.Executor(c)
				s:=fixture.ErrorDiagnostic(tc.sql,tc.diagnostic);s.ErrorStage="execute"
				if prepared {
					if tc.prepareError {s.Prepared,s.ErrorStage=true,"prepare"} else {x=held}
				}
				pgSeclabelExec(t,p,b,c,"BEGIN","SAVEPOINT sls_recover")
				fixture.RunStep(t,p.Ctx,x,b.Recorder,s)
				pgSeclabelsQueryError(t,p,b,c,"SELECT count(*) FROM pg_catalog.pg_seclabels",pgSeclabelAborted())
				pgSeclabelExec(t,p,b,c,"ROLLBACK TO SAVEPOINT sls_recover")
				pgSeclabelsSentinel(t,p,b,c)
				pgSeclabelExec(t,p,b,c,"COMMIT")
			}
		}
		q:="SECURITY LABEL FOR missing ON TABLE sls_private.sentinel IS NULL"
		held:=pgSeclabelHeld(t,p,c,q)
		for _,x:=range []fixture.Executor{c,held}{
			pgSeclabelExec(t,p,b,c,"BEGIN READ ONLY","SAVEPOINT sls_recover")
			pgSeclabelError(t,p,b,x,q,pgSeclabelReadOnly())
			pgSeclabelsQueryError(t,p,b,c,"SELECT count(*) FROM pg_catalog.pg_seclabels",pgSeclabelAborted())
			pgSeclabelExec(t,p,b,c,"ROLLBACK TO SAVEPOINT sls_recover")
			pgSeclabelsSentinel(t,p,b,c)
			pgSeclabelExec(t,p,b,c,"COMMIT")
		}
	})
}

func TestReferencePGSeclabelsFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"sls_acl_reader"})
	pgSeclabelsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		pgSeclabelsSetup(t,p,b,c)
		held:=pgSeclabelHeld(t,p,c,pgSeclabelsSelect)
		for _,x:=range []fixture.Executor{c,held}{fixture.RunStep(t,p.Ctx,x,b.Recorder,pgSeclabelsMetadata(b,pgSeclabelsSelect))}
		pgSeclabelsShape(t,p,b,c)
		q:="SECURITY LABEL ON TABLE sls_private.sentinel IS 'first'"
		set:=pgSeclabelHeld(t,p,c,q)
		pgSeclabelError(t,p,b,set,q,pgSeclabelAbsent())
		pgSeclabelError(t,p,b,c,`SECURITY LABEL FOR "MiSsInG" ON TABLE sls_private.sentinel IS NULL`,pgSeclabelProvider("MiSsInG"))
		pgSeclabelsSentinel(t,p,b,c)
		pgSeclabelsAuthorization(t,p,b,c)
		pgSeclabelError(t,p,b,c,"UPDATE pg_catalog.pg_seclabels SET label='x' WHERE FALSE",pgSeclabelsWriteDiagnostic("update"))
		pgSeclabelsSentinel(t,p,b,c)
		fixture.RunStep(t,p.Ctx,held,b.Recorder,pgSeclabelsMetadata(b,held.SQL))
	})
}
