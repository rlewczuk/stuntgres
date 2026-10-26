package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAggregateOrderedAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"ua_owner","ua_reader"})
	createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		exec:=func(q string){createAggregateRun(t,p,b,c,fixture.Exec(q,0))}
		for _,q:=range []string{
			"CREATE ROLE ua_owner","CREATE ROLE ua_reader","GRANT USAGE,CREATE ON SCHEMA public TO ua_owner","GRANT USAGE ON SCHEMA public TO ua_reader",
			"CREATE FUNCTION ua_auth_step(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT coalesce($1,0)+coalesce($2,0)$$",
			"CREATE FUNCTION ua_auth_final(integer,text) RETURNS text LANGUAGE SQL AS $$SELECT $2||coalesce($1::text,'empty')$$",
			"REVOKE EXECUTE ON FUNCTION ua_auth_final(integer,text) FROM PUBLIC","SET SESSION AUTHORIZATION ua_owner",
		}{exec(q)}
		defer c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
		const ddl="CREATE AGGREGATE ua_rejected(text ORDER BY integer) (SFUNC=ua_auth_step,STYPE=integer,FINALFUNC=ua_auth_final)"
		s:=fixture.ImplicitError(ddl,fixture.Diagnostic{Code:"42501",Message:"permission denied for function ua_auth_final"});s.ErrorStage="execute";createAggregateBoth(t,p,b,c,s);aggregateOrderedNoResidue(t,p,b,c)
		for _,q:=range []string{"RESET SESSION AUTHORIZATION","GRANT EXECUTE ON FUNCTION ua_auth_final(integer,text) TO ua_owner","REVOKE EXECUTE ON FUNCTION ua_auth_step(integer,integer) FROM PUBLIC","SET SESSION AUTHORIZATION ua_owner"}{exec(q)}
		s=fixture.ImplicitError(ddl,fixture.Diagnostic{Code:"42501",Message:"permission denied for function ua_auth_step"});s.ErrorStage="execute";createAggregateBoth(t,p,b,c,s);aggregateOrderedNoResidue(t,p,b,c)
		for _,q:=range []string{"RESET SESSION AUTHORIZATION","GRANT EXECUTE ON FUNCTION ua_auth_step(integer,integer) TO ua_owner","SET SESSION AUTHORIZATION ua_owner",ddl,"ALTER AGGREGATE ua_rejected(text ORDER BY integer) RENAME TO ua_owned","REVOKE EXECUTE ON FUNCTION ua_owned(text,integer) FROM PUBLIC","GRANT EXECUTE ON FUNCTION ua_owned(text,integer) TO ua_reader","RESET SESSION AUTHORIZATION","SET SESSION AUTHORIZATION ua_reader"}{exec(q)}
		const q="SELECT ua_owned($1) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1),(2),(3)) s(x) WHERE x<=$2"
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		check:=func(){for _,tc:=range []struct{limit int64;want string}{{3,"p:6"},{0,"p:empty"}}{s:=aggregateFinalText(q,tc.want,"p:",tc.limit);createAggregateBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}}
		// Aggregate EXECUTE does not require the caller to execute its supports directly.
		check()
		for _,support:=range []struct{signature,name string}{{"ua_auth_step(integer,integer)","ua_auth_step"},{"ua_auth_final(integer,text)","ua_auth_final"}}{
			for _,sql:=range []string{"RESET SESSION AUTHORIZATION","REVOKE EXECUTE ON FUNCTION "+support.signature+" FROM ua_owner","SET SESSION AUTHORIZATION ua_reader"}{exec(sql)}
			for _,limit:=range []int64{3,0}{s:=fixture.ImplicitError(q,fixture.Diagnostic{Code:"42501",Message:"permission denied for function "+support.name},"p:",limit);s.SelectRows=true;s.ErrorStage="execute";createAggregateBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}
			for _,sql:=range []string{"RESET SESSION AUTHORIZATION","GRANT EXECUTE ON FUNCTION "+support.signature+" TO ua_owner","SET SESSION AUTHORIZATION ua_reader"}{exec(sql)};check()
		}
		for _,sql:=range []string{"RESET SESSION AUTHORIZATION","REVOKE EXECUTE ON FUNCTION ua_owned(text,integer) FROM ua_reader","SET SESSION AUTHORIZATION ua_reader"}{exec(sql)}
		s=fixture.ImplicitError(q,fixture.Diagnostic{Code:"42501",Message:"permission denied for aggregate ua_owned"},"p:",0);s.SelectRows=true;s.ErrorStage="execute";fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
		exec("RESET SESSION AUTHORIZATION")
		for _,sql:=range []string{
			"CREATE TABLE ua_private(v integer)",
			"CREATE FUNCTION ua_body_final(integer,text) RETURNS text BEGIN ATOMIC SELECT $2||($1+v)::text FROM ua_private; END",
			"GRANT SELECT ON ua_private TO ua_owner","SET SESSION AUTHORIZATION ua_owner",
			"CREATE AGGREGATE ua_body(text ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC=ua_body_final)",
			"RESET SESSION AUTHORIZATION",
		}{exec(sql)}
		createAggregateRun(t,p,b,c,fixture.Exec("INSERT INTO ua_private VALUES(10)",1));exec("SET SESSION AUTHORIZATION ua_reader")
		body:=fixture.ImplicitError("SELECT ua_body('p:') WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1)) s(x)",fixture.Diagnostic{Code:"42501",Message:"permission denied for table ua_private",Context:"SQL function \"ua_body_final\" statement 1"});body.SelectRows=true;body.ErrorStage="execute";createAggregateBoth(t,p,b,c,body)
		for _,sql:=range []string{"RESET SESSION AUTHORIZATION","GRANT SELECT ON ua_private TO ua_reader","SET SESSION AUTHORIZATION ua_reader"}{exec(sql)}
		createAggregateBoth(t,p,b,c,aggregateFinalText(body.Query,"p:11"));exec("RESET SESSION AUTHORIZATION")
		for _,sql:=range []string{
			"CREATE DOMAIN ua_auth_result AS text","CREATE FUNCTION ua_domain_final(integer,text) RETURNS ua_auth_result LANGUAGE SQL AS $$SELECT ($2||$1::text)::ua_auth_result$$",
			"REVOKE USAGE ON TYPE ua_auth_result FROM PUBLIC","SET SESSION AUTHORIZATION ua_owner",
		}{exec(sql)}
		const domainDDL="CREATE AGGREGATE ua_rejected(text ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC=ua_domain_final)"
		s=fixture.ImplicitError(domainDDL,fixture.Diagnostic{Code:"42501",Message:"permission denied for type ua_auth_result"});s.ErrorStage="execute";createAggregateBoth(t,p,b,c,s);aggregateOrderedNoResidue(t,p,b,c)
		for _,sql:=range []string{"RESET SESSION AUTHORIZATION","GRANT USAGE ON TYPE ua_auth_result TO ua_owner","SET SESSION AUTHORIZATION ua_owner",domainDDL}{exec(sql)}
		createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_rejected('p:') WITHIN GROUP(ORDER BY x)::text AS v FROM(VALUES(1),(2),(3)) s(x)","p:6"));exec("RESET SESSION AUTHORIZATION")
	})
	t.Run("strict_null_live_support",aggregateOrderedStrictAuthority)
	t.Run("variadic_live_support",aggregateOrderedVariadicAuthority)
	t.Run("type_usage_slots",aggregateOrderedTypeAuthorization)
}

func aggregateOrderedStrictAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"ua_strict_owner"})
	createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		exec:=func(q string){createAggregateRun(t,p,b,c,fixture.Exec(q,0))}
		for _,q:=range []string{"CREATE ROLE ua_strict_owner","GRANT USAGE,CREATE ON SCHEMA public TO ua_strict_owner","CREATE FUNCTION ua_strict_final(integer,integer) RETURNS integer STRICT LANGUAGE SQL AS $$SELECT $1+$2$$","REVOKE EXECUTE ON FUNCTION ua_strict_final(integer,integer) FROM PUBLIC","GRANT EXECUTE ON FUNCTION ua_strict_final(integer,integer) TO ua_strict_owner","SET SESSION AUTHORIZATION ua_strict_owner","CREATE AGGREGATE ua_strict_owned(integer ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,FINALFUNC=ua_strict_final)","RESET SESSION AUTHORIZATION"}{exec(q)}
		defer c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
		const q="SELECT ua_strict_owned($1) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(NULL::integer)) s(x) WHERE $2::boolean"
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close();held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		for _,populated:=range []bool{true,false}{fixture.RunStep(t,p.Ctx,held,b.Recorder,createAggregateValue(q,nil,nil,populated))}
		exec("REVOKE EXECUTE ON FUNCTION ua_strict_final(integer,integer) FROM ua_strict_owner")
		for _,populated:=range []bool{true,false}{s:=fixture.ImplicitError(q,fixture.Diagnostic{Code:"42501",Message:"permission denied for function ua_strict_final"},nil,populated);s.SelectRows=true;s.ErrorStage="execute";fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}
		exec("GRANT EXECUTE ON FUNCTION ua_strict_final(integer,integer) TO ua_strict_owner")
		for _,populated:=range []bool{true,false}{s:=createAggregateValue(q,nil,3,populated);createAggregateBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}
	})
}

func aggregateOrderedVariadicAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"ua_variadic_owner"})
	createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		exec:=func(q string){createAggregateRun(t,p,b,c,fixture.Exec(q,0))}
		for _,q:=range []string{"CREATE ROLE ua_variadic_owner","GRANT USAGE,CREATE ON SCHEMA public TO ua_variadic_owner","SET SESSION AUTHORIZATION ua_variadic_owner"}{exec(q)}
		defer c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
		const declaration=`(VARIADIC "any" ORDER BY VARIADIC "any") (SFUNC=pg_catalog.ordered_set_transition_multi,STYPE=internal,FINALFUNC=pg_catalog.rank_final,FINALFUNC_EXTRA,HYPOTHETICAL)`
		for _,prepared:=range []bool{false,true} {
			s:=fixture.ImplicitError("CREATE AGGREGATE ua_rejected"+declaration,fixture.Diagnostic{Code:"42P13",Message:"aggregate transition data type cannot be internal"})
			s.Prepared=prepared;s.ErrorStage="execute";fixture.RunStep(t,p.Ctx,c,b.Recorder,s);aggregateOrderedNoResidue(t,p,b,c)
		}
		exec("RESET SESSION AUTHORIZATION")
		exec("CREATE AGGREGATE ua_rank"+declaration)
		exec(`ALTER AGGREGATE ua_rank(VARIADIC "any" ORDER BY VARIADIC "any") OWNER TO ua_variadic_owner`)
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT p.proowner=r.oid AS owner FROM(SELECT proowner FROM pg_proc WHERE proname='ua_rank') p JOIN pg_roles r ON r.rolname='ua_variadic_owner'",[]string{"owner"},[]string{"BOOL"},[][]any{{true}}))
		defer func(){c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION");c.ExecContext(p.Ctx,"GRANT EXECUTE ON FUNCTION pg_catalog.rank_final(internal,\"any\") TO PUBLIC")}()
		const q="SELECT ua_rank(20) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(10),(20),(40)) s(x) WHERE $1::boolean"
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close();held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		exec("REVOKE EXECUTE ON FUNCTION pg_catalog.rank_final(internal,\"any\") FROM PUBLIC")
		for _,populated:=range []bool{true,false}{s:=fixture.ImplicitError(q,fixture.Diagnostic{Code:"42501",Message:"permission denied for function rank_final"},populated);s.SelectRows=true;s.ErrorStage="execute";fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}
		exec("GRANT EXECUTE ON FUNCTION pg_catalog.rank_final(internal,\"any\") TO PUBLIC")
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,[]string{"v"},[]string{"INT8"},[][]any{{int64(2)}},true))
	})
}

func TestReferenceAggregateOrderedDependencies(t *testing.T) {
	createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		aggregateOrderedSetup(t,p,b,c)
		createAggregateRun(t,p,b,c,
			fixture.Exec("CREATE TABLE ua_direct_source(prefix text)",0),
			fixture.Exec("INSERT INTO ua_direct_source VALUES('p:')",1),
			fixture.Exec("CREATE VIEW ua_dependency_view AS SELECT ua_sorted(prefix,false) WITHIN GROUP(ORDER BY x) AS v FROM ua_direct_source CROSS JOIN ua_input GROUP BY prefix",0),
			fixture.Exec("CREATE FUNCTION ua_dependency_frozen() RETURNS text BEGIN ATOMIC SELECT ua_sorted(prefix,false) WITHIN GROUP(ORDER BY x) FROM ua_direct_source CROSS JOIN ua_input GROUP BY prefix; END",0))
		for _,relation:=range []string{"ua_direct_source","ua_input"}{
			createAggregateBoth(t,p,b,c,fixture.Select("SELECT EXISTS(SELECT 1 FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='ua_dependency_frozen()'::regprocedure AND refclassid='pg_class'::regclass AND refobjid='"+relation+"'::regclass) AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
		}
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT d.deptype::text AS kind FROM(SELECT oid FROM pg_proc WHERE proname='ua_sorted') p JOIN pg_depend d ON d.objid=p.oid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_proc'::regclass AND d.refobjid='ua_finish(integer[],text,boolean,integer)'::regprocedure",[]string{"kind"},[]string{"TEXT"},[][]any{{"n"}}))
		createAggregateRun(t,p,b,c,fixture.ImplicitError("DROP FUNCTION ua_finish(integer[],text,boolean,integer)",fixture.Diagnostic{Code:"2BP01",Message:"cannot drop function ua_finish(integer[],text,boolean,integer) because other objects depend on it",Detail:"function ua_sorted(text,boolean,integer) depends on function ua_finish(integer[],text,boolean,integer)\nview ua_dependency_view depends on function ua_sorted(text,boolean,integer)\nfunction ua_dependency_frozen() depends on function ua_sorted(text,boolean,integer)",Hint:"Use DROP ... CASCADE to drop the dependent objects too."}))
		createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT v FROM ua_dependency_view","p:1,2,3,NULL;extra=true"))
		for _,relation:=range []string{"ua_direct_source","ua_input"} {
			createAggregateRun(t,p,b,c,fixture.ImplicitError("DROP TABLE "+relation,fixture.Diagnostic{Code:"2BP01",Message:"cannot drop table "+relation+" because other objects depend on it",Detail:"view ua_dependency_view depends on table "+relation+"\nfunction ua_dependency_frozen() depends on table "+relation,Hint:"Use DROP ... CASCADE to drop the dependent objects too."}))
		}
		createAggregateRun(t,p,b,c,fixture.Exec("DROP TABLE ua_direct_source CASCADE",0),fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='ua_dependency_view'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}),fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='ua_dependency_frozen'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
		createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input","p:1,2,3,NULL;extra=true"))
		createAggregateRun(t,p,b,c,
			fixture.Exec("CREATE VIEW ua_row_view AS SELECT ua_sorted('row:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input",0),
			fixture.Exec("CREATE FUNCTION ua_row_frozen() RETURNS text BEGIN ATOMIC SELECT ua_sorted('row:',false) WITHIN GROUP(ORDER BY x) FROM ua_input; END",0),
			fixture.Exec("DROP TABLE ua_input CASCADE",0),
			fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='ua_row_view'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}),
			fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='ua_row_frozen'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
		createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1),(2),(3)) s(x)","p:1,2,3;extra=true"))
	})
}
