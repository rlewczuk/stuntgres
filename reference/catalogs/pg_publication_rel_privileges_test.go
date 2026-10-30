package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func publicationRelACL(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,public,reader bool) {
	t.Helper()
	var want [][]any
	for _, privilege:=range []string{"DELETE","INSERT","MAINTAIN","REFERENCES","SELECT","TRIGGER","TRUNCATE","UPDATE"} { want=append(want,[]any{true,true,false,privilege,false}) }
	if public { want=append(want,[]any{true,false,true,"SELECT",false}) }
	if reader { want=append(want,[]any{true,false,false,"SELECT",false}) }
	s:=fixture.Select("SELECT a.grantor=c.relowner AS owner_grantor,a.grantee=c.relowner AS owner_grantee,a.grantee=0 AS public_grantee,a.privilege_type,a.is_grantable FROM pg_catalog.pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a WHERE c.oid=6106",[]string{"owner_grantor","owner_grantee","public_grantee","privilege_type","is_grantable"},[]string{"BOOL","BOOL","BOOL","TEXT","BOOL"},want)
	s.Bag=true
	publicationRun(t,p,b,e,fixture.Select("SELECT relacl IS NOT NULL AS explicit_acl FROM pg_catalog.pg_class WHERE oid=6106",[]string{"explicit_acl"},[]string{"BOOL"},[][]any{{true}}),s,fixture.Prepared(s))
}

func TestReferencePGPublicationRelPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"publication_rel_reader"})
	publicationScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		admin,actor:=fixture.LockConnections(t,p.Ctx,b.DB)
		run:=func(s ...fixture.Step){publicationRun(t,p,b,admin,s...)}
		run(fixture.Exec("CREATE ROLE publication_rel_reader",0))
		defer func(){
			ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second)
			defer cancel()
			for _,q:=range []string{"ROLLBACK","GRANT SELECT ON pg_catalog.pg_publication_rel TO PUBLIC","REVOKE SELECT ON pg_catalog.pg_publication_rel FROM publication_rel_reader"} { if _,err:=admin.ExecContext(ctx,q);err!=nil { t.Error(err) } }
			if _,err:=actor.ExecContext(ctx,"RESET ROLE");err!=nil { t.Error(err) }
		}()
		publicationRelPayloadSetup(t,p,b,admin)
		publicationRun(t,p,b,actor,fixture.Exec("SET ROLE publication_rel_reader",0))
		steps:=[]fixture.Step{
			publicationRelState([]any{"publication_rt_p","publication_rt_t","1 3","(v > 0)"}),
			fixture.Select("SELECT prattrs::text FROM pg_catalog.pg_publication_rel",[]string{"prattrs"},[]string{"TEXT"},[][]any{{"1 3"}}),
			fixture.Select("SELECT * FROM pg_catalog.pg_publication_rel WHERE FALSE",[]string{"oid","prpubid","prrelid","prqual","prattrs"},[]string{"OID","OID","OID","PG_NODE_TREE","INT2VECTOR"},nil),
			fixture.Select("SELECT * FROM pg_catalog.pg_publication_rel LIMIT 0",[]string{"oid","prpubid","prrelid","prqual","prattrs"},[]string{"OID","OID","OID","PG_NODE_TREE","INT2VECTOR"},nil),
			fixture.Select("SELECT count(*) FROM pg_catalog.pg_publication_rel",[]string{"count"},[]string{"INT8"},[][]any{{int64(1)}}),
		}
		held:=make([]fixture.HeldStatement,len(steps))
		for i,s:=range steps {
			stmt,err:=actor.PrepareContext(p.Ctx,s.Query)
			if err!=nil { t.Fatal(err) }
			defer stmt.Close()
			held[i]=fixture.HeldStatement{Executor:actor,Statement:stmt,SQL:s.Query}
		}
		check:=func(allowed,public,reader bool){
			t.Helper()
			publicationRelACL(t,p,b,admin,public,reader)
			run(fixture.Select("SELECT has_table_privilege('publication_rel_reader','pg_catalog.pg_publication_rel','SELECT') AS catalog_read,has_table_privilege('publication_rel_reader','publication_rt_t','SELECT') AS user_read",[]string{"catalog_read","user_read"},[]string{"BOOL","BOOL"},[][]any{{allowed,false}}),fixture.Select("SELECT c.relowner=r.oid AS reader_owner FROM pg_catalog.pg_class c CROSS JOIN pg_catalog.pg_roles r WHERE c.relname='publication_rt_t' AND r.rolname='publication_rel_reader'",[]string{"reader_owner"},[]string{"BOOL"},[][]any{{false}}))
			for i,s:=range steps {
				for _,e:=range []fixture.Executor{actor,held[i]} {
					if allowed { publicationRun(t,p,b,e,s) } else {
						denied:=fixture.ErrorDiagnostic(s.Query,fixture.ControlsReferenceDiagnostic("42501","permission denied for table pg_publication_rel"))
						denied.SelectRows,denied.ErrorStage=true,"execute"
						publicationRun(t,p,b,e,denied)
					}
				}
				if allowed { publicationRun(t,p,b,actor,fixture.Prepared(s)) } else {
					denied:=fixture.ErrorDiagnostic(s.Query,fixture.ControlsReferenceDiagnostic("42501","permission denied for table pg_publication_rel"))
					denied.SelectRows,denied.Prepared,denied.ErrorStage=true,true,"execute"
					publicationRun(t,p,b,actor,denied)
				}
			}
			run(steps[0])
		}
		check(true,true,false)
		run(fixture.Exec("REVOKE SELECT ON pg_catalog.pg_publication_rel FROM PUBLIC",0));check(false,false,false)
		run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_publication_rel TO publication_rel_reader",0));check(true,false,true)
		run(fixture.Exec("REVOKE SELECT ON pg_catalog.pg_publication_rel FROM publication_rel_reader",0));check(false,false,false)
		run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_publication_rel TO PUBLIC",0));check(true,true,false)
		for _,tc:=range []struct{query,state,message string}{
			{"SELECT missing FROM pg_catalog.pg_publication_rel","42703",`column "missing" does not exist`},
			{"SELECT 'bad'::oid FROM pg_catalog.pg_publication_rel","22P02",`invalid input syntax for type oid: "bad"`},
		} {
			for _,prepared:=range []bool{false,true} {
				d:=fixture.ControlsReferenceDiagnostic(tc.state,tc.message);d.Position="8"
				s:=fixture.ErrorDiagnostic(tc.query,d)
				s.SelectRows,s.Prepared,s.ErrorStage=true,prepared,"execute"
				if prepared { s.ErrorStage="prepare" }
				publicationRun(t,p,b,actor,s)
			}
		}
	})
}
