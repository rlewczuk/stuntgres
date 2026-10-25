package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAccessExecutionTemporaryPersistence(t *testing.T) {
	var emitted string
	objectGrantScenario(t,nil,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,run func(fixture.Step),prepared bool){
		defer accessExecutionResetConnection(t,c)
		if b.PG {
			if emitted=="" {t.Fatal("missing actual temporary-omission native stream")}
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC",0))
			if _,err:=c.ExecContext(p.Ctx,emitted);err!=nil {t.Fatal(err)}
			run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		} else {
			for _,q:=range []string{
				"CREATE FUNCTION ae_temp_keep() RETURNS integer RETURN 1",
				"REVOKE EXECUTE ON FUNCTION ae_temp_keep() FROM PUBLIC",
				"GRANT EXECUTE ON FUNCTION ae_temp_keep() TO og_object_reader WITH GRANT OPTION",
			} {run(fixture.Exec(q,0))}
		}
		persistent:=func(db fixture.Executor,recorder *fixture.Recorder){
			fixture.RunStep(t,p.Ctx,db,recorder,objectGrantAccessValue("SELECT has_function_privilege('og_object_reader','public.ae_temp_keep()','EXECUTE WITH GRANT OPTION') AS v",true))
			fixture.RunStep(t,p.Ctx,db,recorder,objectGrantAccessValue("SELECT has_function_privilege('public','public.ae_temp_keep()','EXECUTE') AS v",false))
			step:=fixture.Select("SELECT unnest(pg_get_acl(1255,'public.ae_temp_keep()'::regprocedure,0))::text AS acl",[]string{"acl"},[]string{"TEXT"},[][]any{{"postgres=X/postgres"},{"og_object_reader=X*/postgres"}})
			step.Bag=true;fixture.RunStep(t,p.Ctx,db,recorder,step)
			fixture.RunStep(t,p.Ctx,db,recorder,fixture.Select("SELECT ae_temp_keep() AS v",[]string{"v"},[]string{"INT4"},[][]any{{int64(1)}}))
		}
		absent:=fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname IN('ae_temp_null','ae_temp_explicit')",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
		run(absent)
		persistent(c,b.Recorder)
		run(fixture.Exec("CREATE FUNCTION pg_temp.ae_temp_explicit() RETURNS integer RETURN 3",0))
		run(fixture.Select("SELECT proname,pronamespace=pg_my_temp_schema() AS temporary FROM pg_proc WHERE proname='ae_temp_explicit'",[]string{"proname","temporary"},[]string{"NAME","BOOL"},[][]any{{"ae_temp_explicit",true}}))
		var temporaryNamespace string
		if err:=c.QueryRowContext(p.Ctx,"SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&temporaryNamespace);err!=nil {t.Fatal(err)}
		temporaryNull:=pq.QuoteIdentifier(temporaryNamespace)+".ae_temp_null"
		temporaryExplicit:=pq.QuoteIdentifier(temporaryNamespace)+".ae_temp_explicit"
		// Existing text routine selection excludes temporary namespaces, including
		// qualified names. Schema-wide grants enumerate the real descriptors;
		// apply them before creating the absent-ACL sibling, then inspect by OID.
		run(fixture.Exec("REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA "+pq.QuoteIdentifier(temporaryNamespace)+" FROM PUBLIC",0))
		run(fixture.Exec("GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA "+pq.QuoteIdentifier(temporaryNamespace)+" TO og_object_reader WITH GRANT OPTION",0))
		run(fixture.Exec("CREATE FUNCTION pg_temp.ae_temp_null() RETURNS integer RETURN 2",0))
		var temporaryNullOID,temporaryExplicitOID int64
		if err:=c.QueryRowContext(p.Ctx,"SELECT oid FROM pg_proc WHERE pronamespace=pg_my_temp_schema() AND proname='ae_temp_null'").Scan(&temporaryNullOID);err!=nil {t.Fatal(err)}
		if err:=c.QueryRowContext(p.Ctx,"SELECT oid FROM pg_proc WHERE pronamespace=pg_my_temp_schema() AND proname='ae_temp_explicit'").Scan(&temporaryExplicitOID);err!=nil {t.Fatal(err)}
		defer func(){
			for _,q:=range []string{"DROP FUNCTION IF EXISTS "+temporaryNull+"()","DROP FUNCTION IF EXISTS "+temporaryExplicit+"()"} {
				if _,err:=c.ExecContext(context.Background(),q);err!=nil {t.Error("temporary inquiry routine cleanup",err)}
			}
		}()
		temporary:=fixture.Select("SELECT proname,pronamespace=pg_my_temp_schema() AS temporary FROM pg_proc WHERE proname IN('ae_temp_null','ae_temp_explicit') ORDER BY proname",[]string{"proname","temporary"},[]string{"NAME","BOOL"},[][]any{{"ae_temp_explicit",true},{"ae_temp_null",true}})
		run(temporary)
		var owner string
		if err:=c.QueryRowContext(p.Ctx,"SELECT current_user").Scan(&owner);err!=nil {t.Fatal(err)}
		raw:=fixture.Select("SELECT unnest(pg_get_acl(1255,$1::oid,0))::text AS acl",[]string{"acl"},[]string{"TEXT"},[][]any{{owner+"=X/"+owner},{"og_object_reader=X*/"+owner}},temporaryExplicitOID)
		raw.Bag=true;run(raw)
		run(objectGrantAccessValue("SELECT pg_get_acl(1255,$1::oid,0) IS NULL AS v",true,temporaryNullOID))
		run(objectGrantAccessValue("SELECT pg_get_acl(1255,$1::oid,0) IS NULL AS v",false,temporaryExplicitOID))
		run(objectGrantAccessValue("SELECT has_function_privilege('og_object_reader',$1::oid,'EXECUTE') AS v",true,temporaryNullOID))
		run(objectGrantAccessValue("SELECT has_function_privilege('og_object_reader',$1::oid,'EXECUTE WITH GRANT OPTION') AS v",true,temporaryExplicitOID))
		if b.PG {return}
		var saved bytes.Buffer
		if err:=stuntgres.Dump(p.Ctx,p.Native,&saved);err!=nil {t.Fatal(err)}
		emitted=saved.String()
		for _,name:=range []string{"ae_temp_null","ae_temp_explicit",temporaryNamespace} {
			if strings.Contains(emitted,name) {t.Fatal("Dump leaked temporary declaration or ACL repair",name)}
		}
		if !strings.Contains(emitted,"ae_temp_keep") || !strings.Contains(emitted,"SET proacl=") {t.Fatal("persistent declaration or raw ACL repair missing")}
		connector,err:=stuntgres.NewDriver().OpenConnector("access-execution-temporary/"+t.Name());if err!=nil {t.Fatal(err)}
		fresh:=sql.OpenDB(connector);defer fresh.Close()
		recorded,recorder:=fixture.RecordedNativePool(t,connector)
		for _,q:=range []string{"CREATE ROLE og_object_reader","CREATE ROLE og_object_other"} {fixture.RunStep(t,p.Ctx,recorded,recorder,fixture.Exec(q,0))}
		if err:=stuntgres.Restore(p.Ctx,fresh,bytes.NewReader(saved.Bytes()));err!=nil {t.Fatal(err)}
		fixture.RunStep(t,p.Ctx,recorded,recorder,absent)
		persistent(recorded,recorder)
		// Capturing and replaying elsewhere cannot mutate the source temps.
		run(temporary)
		run(raw)
		run(objectGrantAccessValue("SELECT pg_get_acl(1255,$1::oid,0) IS NULL AS v",true,temporaryNullOID))
	})
}
