package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

type pseudoIdentity struct { id int64; name string }

var pseudoIdentities = []pseudoIdentity{
	{2276,"any"}, {2283,"anyelement"}, {2277,"anyarray"}, {2776,"anynonarray"}, {3500,"anyenum"}, {3831,"anyrange"}, {4537,"anymultirange"},
	{5077,"anycompatible"}, {5078,"anycompatiblearray"}, {5079,"anycompatiblenonarray"}, {5080,"anycompatiblerange"}, {4538,"anycompatiblemultirange"},
	{2275,"cstring"}, {2281,"internal"}, {2280,"language_handler"}, {3115,"fdw_handler"}, {269,"table_am_handler"}, {325,"index_am_handler"}, {3310,"tsm_handler"},
	{2249,"record"}, {2279,"trigger"}, {3838,"event_trigger"}, {32,"pg_ddl_command"}, {2278,"void"}, {705,"unknown"},
}

func pseudoError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, want fixture.Diagnostic, selecting bool) {
	t.Helper()
	for _, prepared := range []bool{false,true} {
		s := fixture.ImplicitError(query, want)
		s.SelectRows = selecting
		s.ErrorStage = "execute"
		if prepared && selecting && want.Context == "" { s.ErrorStage = "prepare" }
		if prepared { s = fixture.Prepared(s) }
		fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
	}
}

func TestReferencePseudoTypesMetadata(t *testing.T) {
	routineScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		for _, identity := range pseudoIdentities {
			category := "P"; if identity.id == 705 { category = "X" }
			pseudoCatalogFacts(t,p,b,c,identity.id)
			q := fmt.Sprintf("SELECT oid::integer,typname,typtype::text,typcategory::text,typisdefined,typrelid::integer,typelem::integer,typcollation::integer FROM pg_type WHERE oid=%d",identity.id)
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select(q,[]string{"oid","typname","typtype","typcategory","typisdefined","typrelid","typelem","typcollation"},[]string{"INT4","NAME","TEXT","TEXT","BOOL","INT4","INT4","INT4"},[][]any{{identity.id,identity.name,"p",category,true,int64(0),int64(0),int64(0)}}))
		}
		routineExec(t,p,b,c,"CREATE FUNCTION pseudo_id(anyelement) RETURNS anyelement LANGUAGE SQL AS $$SELECT $1$$")
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT proargtypes::text,prorettype::integer FROM pg_proc WHERE oid='pseudo_id(anyelement)'::regprocedure",[]string{"proargtypes","prorettype"},[]string{"TEXT","INT4"},[][]any{{"2283",int64(2283)}}))
		receiver := fixture.Select("SELECT proname,pronamespace::integer,pronargs,prorettype::integer,provolatile::text,proisstrict,proparallel::text,prokind::text,proargnames,prolang::integer,proretset,prorows,prosrc,proargtypes::text,provariadic::integer FROM pg_proc WHERE oid=88",
			[]string{"proname","pronamespace","pronargs","prorettype","provolatile","proisstrict","proparallel","prokind","proargnames","prolang","proretset","prorows","prosrc","proargtypes","provariadic"},
			[]string{"NAME","INT4","INT2","INT4","TEXT","BOOL","TEXT","TEXT","_TEXT","INT4","BOOL","FLOAT4","TEXT","TEXT","INT4"},
			[][]any{{"pg_ddl_command_recv",int64(11),int64(1),int64(32),"i",true,"s","f",nil,int64(12),false,float64(0),"pg_ddl_command_recv","2281",int64(0)}})
		fixture.RunStep(t,p.Ctx,c,b.Recorder,receiver)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(receiver))
	})
}

func TestReferencePseudoTypesStorage(t *testing.T) {
	for _, identity := range pseudoIdentities {
		t.Run(identity.name,func(t *testing.T) {
			routineScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				name := identity.name; if name == "any" { name = `"any"` }
				want := fixture.Diagnostic{Code:"42P16",Message:`column "v" has pseudo-type `+name}
				pseudoError(t,p,b,c,"CREATE TABLE pseudo_bad(v "+name+")",want,false)
				routineExec(t,p,b,c,"CREATE TABLE pseudo_bad(v integer)")
				pseudoError(t,p,b,c,"ALTER TABLE pseudo_bad ADD COLUMN w "+name,fixture.Diagnostic{Code:"42P16",Message:`column "w" has pseudo-type `+name},false)
				pseudoError(t,p,b,c,"ALTER TABLE pseudo_bad ALTER COLUMN v TYPE "+name,want,false)
				routineValueWithNativeNullability(t,p,b,c,"SELECT count(*) FROM pseudo_bad","count","INT8",int64(0),false,true)
			})
		})
	}
	routineScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE pseudo_literal AS SELECT 'x' AS v",1))
		routineValueWithNativeNullability(t,p,b,c,"SELECT v FROM pseudo_literal","v","TEXT","x",true,true)
		pseudoError(t,p,b,c,"CREATE VIEW pseudo_void AS SELECT NULL::void AS v",fixture.Diagnostic{Code:"42P16",Message:`column "v" has pseudo-type void`},false)
		pseudoError(t,p,b,c,"CREATE TYPE pseudo_composite AS (v anyelement)",fixture.Diagnostic{Code:"42P16",Message:`column "v" has pseudo-type anyelement`},false)
	})
}

func TestReferencePseudoTypesDeclarations(t *testing.T) {
	routineScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		for _, name := range []string{"cstring","internal","language_handler","fdw_handler","table_am_handler","index_am_handler","tsm_handler","trigger","event_trigger","pg_ddl_command","void","unknown","any"} {
			spelling := name
			if name == "any" { spelling = `"any"` }
			pseudoError(t,p,b,c,"CREATE FUNCTION pseudo_forbidden("+spelling+") RETURNS integer LANGUAGE SQL AS $$SELECT 1$$",fixture.Diagnostic{Code:"42P13",Message:"SQL functions cannot have arguments of type "+spelling},false)
			pseudoError(t,p,b,c,"CREATE FUNCTION pseudo_forbidden("+spelling+") RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 1; END$$",fixture.Diagnostic{Code:"0A000",Message:"PL/pgSQL functions cannot accept type "+spelling},false)
			if name != "void" && name != "trigger" && name != "event_trigger" {
				input := ""
				if name == "internal" { input = "internal" }
				pseudoError(t,p,b,c,"CREATE FUNCTION pseudo_forbidden("+input+") RETURNS "+spelling+" LANGUAGE SQL AS $$SELECT NULL$$",fixture.Diagnostic{Code:"42P13",Message:"SQL functions cannot return type "+spelling},false)
				pseudoError(t,p,b,c,"CREATE FUNCTION pseudo_forbidden("+input+") RETURNS "+spelling+" LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",fixture.Diagnostic{Code:"0A000",Message:"PL/pgSQL functions cannot return type "+spelling},false)
			}
		}
		pseudoError(t,p,b,c,"CREATE FUNCTION pseudo_outvoid(OUT x void) LANGUAGE plpgsql AS $$BEGIN END$$",fixture.Diagnostic{Code:"0A000",Message:"PL/pgSQL functions cannot accept type void"},false)
		pseudoError(t,p,b,c,"CREATE FUNCTION pseudo_sqlrecord(record) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$",fixture.Diagnostic{Code:"42P13",Message:"SQL functions cannot have arguments of type record"},false)
		for _, tc := range []struct{query,code,message,detail string}{
			{"CREATE FUNCTION pseudo_bad() RETURNS anyelement LANGUAGE SQL AS $$SELECT 1$$","42P13","cannot determine result data type","A result of type anyelement requires at least one input of type anyelement, anyarray, anynonarray, anyenum, anyrange, or anymultirange."},
			{"CREATE FUNCTION pseudo_bad(anyelement) RETURNS anyrange LANGUAGE SQL AS $$SELECT NULL$$","42P13","cannot determine result data type","A result of type anyrange requires at least one input of type anyrange or anymultirange."},
			{"CREATE FUNCTION pseudo_bad() RETURNS internal LANGUAGE SQL AS $$SELECT NULL$$","42P13",`unsafe use of pseudo-type "internal"`,"A result of type internal requires at least one input of type internal."},
			{"CREATE FUNCTION pseudo_bad(internal) RETURNS internal LANGUAGE SQL AS $$SELECT $1$$","42P13","SQL functions cannot return type internal",""},
			{"CREATE FUNCTION pseudo_bad(cstring) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$","42P13","SQL functions cannot have arguments of type cstring",""},
			{"CREATE FUNCTION pseudo_bad(anyelement) RETURNS anyelement LANGUAGE SQL RETURN $1","42P13","SQL function with unquoted function body cannot have polymorphic arguments",""},
		} { pseudoError(t,p,b,c,tc.query,fixture.Diagnostic{Code:tc.code,Message:tc.message,Detail:tc.detail},false) }
		pseudoError(t,p,b,c,"CREATE FUNCTION pseudo_cmr(x anycompatiblemultirange) RETURNS anycompatiblemultirange LANGUAGE plpgsql AS $$BEGIN RETURN x; END$$",fixture.Diagnostic{Code:"0A000",Message:"PL/pgSQL functions cannot accept type anycompatiblemultirange",Context:`compilation of PL/pgSQL function "pseudo_cmr" near line 1`},false)
		pseudoError(t,p,b,c,"CREATE FUNCTION pseudo_cmr_out(IN x anycompatiblerange,OUT y anycompatiblemultirange) LANGUAGE plpgsql AS $$BEGIN y:=NULL; END$$",fixture.Diagnostic{Code:"0A000",Message:"PL/pgSQL functions cannot accept type anycompatiblemultirange",Context:`compilation of PL/pgSQL function "pseudo_cmr_out" near line 1`},false)
		pseudoError(t,p,b,c,"CREATE FUNCTION pseudo_cmr_inout(INOUT x anycompatiblemultirange) LANGUAGE plpgsql AS $$BEGIN END$$",fixture.Diagnostic{Code:"0A000",Message:"PL/pgSQL functions cannot accept type anycompatiblemultirange",Context:`compilation of PL/pgSQL function "pseudo_cmr_inout" near line 1`},false)
		routineValueWithNativeNullability(t,p,b,c,"SELECT count(*) FROM pg_proc WHERE proname IN ('pseudo_bad','pseudo_cmr','pseudo_cmr_out','pseudo_cmr_inout')","count","INT8",int64(0),false,true)
		routineExec(t,p,b,c,"CREATE FUNCTION pseudo_bad(anyelement) RETURNS anyelement LANGUAGE SQL AS $$SELECT $1$$")
		routineValue(t,p,b,c,"SELECT pseudo_bad(7)","pseudo_bad","INT4",int64(7))
		pseudoError(t,p,b,c,"CREATE OR REPLACE FUNCTION pseudo_bad(anyelement) RETURNS anyrange LANGUAGE SQL AS $$SELECT NULL$$",fixture.Diagnostic{Code:"42P13",Message:"cannot determine result data type",Detail:"A result of type anyrange requires at least one input of type anyrange or anymultirange."},false)
		routineValue(t,p,b,c,"SELECT pseudo_bad(9)","pseudo_bad","INT4",int64(9))
	})
}

func TestReferencePseudoTypesSimple(t *testing.T) {
	routineScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		routineExec(t,p,b,c,
			"CREATE TYPE pseudo_color AS ENUM ('red','blue')",
			"CREATE FUNCTION pseudo_id(anyelement) RETURNS anyelement LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION pseudo_same(anyelement,anyelement) RETURNS anyelement LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION pseudo_leaf(anyarray,anyelement) RETURNS anyelement LANGUAGE SQL AS $$SELECT $2$$",
			"CREATE FUNCTION pseudo_nonarray(anynonarray) RETURNS anynonarray LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION pseudo_enum(anyenum) RETURNS anyenum LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION pseudo_range(anyrange,anymultirange,anyelement) RETURNS anyelement LANGUAGE SQL AS $$SELECT $3$$")
		for _, tc := range []struct{expression,typ string; value any}{
			{"7","INT4",int64(7)}, {"'x'::text","TEXT","x"}, {"ARRAY[1,2]","_INT4","{1,2}"}, {"'red'::pseudo_color","","red"},
			{"'[1,3)'::int4range","INT4RANGE","[1,3)"}, {"'{[1,3)}'::int4multirange","INT4MULTIRANGE","{[1,3)}"}, {"NULL::integer","INT4",nil},
		} {
			routineValue(t,p,b,c,"SELECT pseudo_id("+tc.expression+") AS v","v",tc.typ,tc.value)
			semantic := ""
			if tc.typ == "" { semantic = "pseudo_color" }
			if tc.typ == "INT4MULTIRANGE" { semantic = "int4multirange" }
			if b.PG && semantic != "" {
				routineValue(t,p,b,c,"SELECT pg_typeof(pseudo_id("+tc.expression+"))::text AS v","v","TEXT",semantic)
			}
		}
		// Both clients leave user enum names empty. Persist the inferred result
		// without a cast, then inspect the declared column's actual identity.
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE pseudo_enum_result AS SELECT pseudo_id('red'::pseudo_color) AS v",1))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT a.atttypid='pseudo_color'::regtype AS exact FROM pg_attribute a WHERE a.attrelid='pseudo_enum_result'::regclass AND a.attname='v'",[]string{"exact"},[]string{"BOOL"},[][]any{{true}}))
		routineValueWithNativeNullability(t,p,b,c,"SELECT v FROM pseudo_enum_result","v","","red",true,true)
		routineValue(t,p,b,c,"SELECT pseudo_leaf(ARRAY[1,2],7)","pseudo_leaf","INT4",int64(7))
		routineValue(t,p,b,c,"SELECT pseudo_enum('red'::pseudo_color)","pseudo_enum","","red")
		routineValue(t,p,b,c,"SELECT pseudo_range('[1,3)'::int4range,'{[1,3)}'::int4multirange,7)","pseudo_range","INT4",int64(7))
		for _, q := range []string{"SELECT pseudo_id(NULL)","SELECT pseudo_id('x')","SELECT pseudo_id($1)"} {
			if q == "SELECT pseudo_id($1)" { _,err := c.PrepareContext(p.Ctx,q); fixture.AssertDiagnostic(t,err,&fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42804",Message:"could not determine polymorphic type because input has type unknown"}); continue }
			pseudoError(t,p,b,c,q,fixture.Diagnostic{Code:"42804",Message:"could not determine polymorphic type because input has type unknown"},true)
		}
		pseudoError(t,p,b,c,"SELECT pseudo_same(1,'x'::text)",fixture.Diagnostic{Code:"42883",Message:"function pseudo_same(integer, text) does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(8)},true)
		pseudoError(t,p,b,c,"SELECT pseudo_nonarray(ARRAY[1])",fixture.Diagnostic{Code:"42883",Message:"function pseudo_nonarray(integer[]) does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(8)},true)
		pseudoError(t,p,b,c,"SELECT pseudo_enum(1)",fixture.Diagnostic{Code:"42883",Message:"function pseudo_enum(integer) does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(8)},true)
	})
}

func TestReferencePseudoTypesCompatible(t *testing.T) {
	routineScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		routineExec(t,p,b,c,
			"CREATE FUNCTION pseudo_first(anycompatible,anycompatible) RETURNS anycompatible LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION pseudo_arrays(anycompatiblearray,anycompatiblearray) RETURNS anycompatiblearray LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION pseudo_cn(anycompatiblenonarray,anycompatiblenonarray) RETURNS anycompatiblenonarray LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION pseudo_cr(anycompatiblerange,anycompatible) RETURNS anycompatiblerange LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION pseudo_sql_cmr(anycompatiblemultirange) RETURNS anycompatiblemultirange LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION pseudo_cmr_result(x anycompatiblerange) RETURNS anycompatiblemultirange LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
			"CREATE FUNCTION pseudo_independent(anyelement,anycompatible,anycompatible) RETURNS anyelement LANGUAGE SQL AS $$SELECT $1$$")
		routineValue(t,p,b,c,"SELECT pseudo_first(1,2.5::numeric)","pseudo_first","NUMERIC","1")
		routineValue(t,p,b,c,"SELECT pseudo_first(NULL,'x')","pseudo_first","TEXT",nil)
		routineValue(t,p,b,c,"SELECT pseudo_first('x','y')","pseudo_first","TEXT","x")
		routineValue(t,p,b,c,"SELECT pseudo_arrays(ARRAY[1],ARRAY[2.5::numeric])","pseudo_arrays","_NUMERIC","{1}")
		routineValue(t,p,b,c,"SELECT pseudo_cn(1,2.5::numeric)","pseudo_cn","NUMERIC","1")
		routineValue(t,p,b,c,"SELECT pseudo_cr('[1,3)'::int4range,2)","pseudo_cr","INT4RANGE","[1,3)")
		routineValue(t,p,b,c,"SELECT pseudo_sql_cmr('{[1,3)}'::int4multirange)","pseudo_sql_cmr","INT4MULTIRANGE","{[1,3)}")
		routineValue(t,p,b,c,"SELECT pseudo_sql_cmr(NULL::int4multirange)","pseudo_sql_cmr","INT4MULTIRANGE",nil)
		routineValue(t,p,b,c,"SELECT pseudo_independent(7,1,2.5::numeric)","pseudo_independent","INT4",int64(7))
		routineValue(t,p,b,c,"SELECT pseudo_cmr_result('[1,3)'::int4range)","pseudo_cmr_result","INT4MULTIRANGE",nil)
		if b.PG {
			// lib/pq omits these OIDs; native names above assert the result identity.
			routineValue(t,p,b,c,"SELECT pg_typeof(pseudo_sql_cmr(NULL::int4multirange))::text AS v","v","TEXT","int4multirange")
			routineValue(t,p,b,c,"SELECT pg_typeof(pseudo_cmr_result('[1,3)'::int4range))::text AS v","v","TEXT","int4multirange")
		}
		pseudoError(t,p,b,c,"SELECT pseudo_cr('[1,3)'::int4range,2.5::numeric)",fixture.Diagnostic{Code:"42883",Message:"function pseudo_cr(int4range, numeric) does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(8)},true)
		query := "SELECT pseudo_sql_cmr($1::int4multirange) AS v"
		statement,err := c.PrepareContext(p.Ctx,query)
		if err != nil { t.Fatal(err) }
		defer statement.Close()
		for _, value := range []any{"{[1,3)}",nil,"{[1,3)}"} {
			step := routineSelect(b,query,"v","INT4MULTIRANGE",value,value)
			step.CheckMetadata = routineScalarMetadata(b,"v","INT4MULTIRANGE",false,false)
			fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:statement,SQL:query},b.Recorder,step)
		}
	})
}

func TestReferencePseudoTypesParameters(t *testing.T) {
	routineScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		routineExec(t,p,b,c,
			"CREATE FUNCTION pseudo_named(a anyelement,b anyelement DEFAULT 9) RETURNS anyelement LANGUAGE SQL AS $$SELECT a$$",
			"CREATE FUNCTION pseudo_out(IN x anyelement,OUT y anyelement) LANGUAGE SQL AS $$SELECT x$$",
			"CREATE FUNCTION pseudo_inout(INOUT x anyelement) LANGUAGE SQL AS $$SELECT x$$",
			"CREATE FUNCTION pseudo_variadic(VARIADIC xs anyarray) RETURNS anyarray LANGUAGE SQL AS $$SELECT xs$$",
			"CREATE FUNCTION pseudo_cv(VARIADIC xs anycompatiblearray) RETURNS anycompatiblearray LANGUAGE SQL AS $$SELECT xs$$")
		routineValue(t,p,b,c,"SELECT pseudo_named(b=>2,a=>7)","pseudo_named","INT4",int64(7))
		routineValue(t,p,b,c,"SELECT pseudo_named(7)","pseudo_named","INT4",int64(7))
		routineValue(t,p,b,c,"SELECT pseudo_out(7)","pseudo_out","INT4",int64(7))
		routineValue(t,p,b,c,"SELECT pseudo_inout('x'::text)","pseudo_inout","TEXT","x")
		routineValue(t,p,b,c,"SELECT pseudo_variadic(1,2)","pseudo_variadic","_INT4","{1,2}")
		routineValue(t,p,b,c,"SELECT pseudo_variadic(VARIADIC ARRAY[1,2])","pseudo_variadic","_INT4","{1,2}")
		routineValue(t,p,b,c,"SELECT pseudo_cv(1,2.5::numeric)","pseudo_cv","_NUMERIC","{1,2.5}")
		routineExec(t,p,b,c,"CREATE SEQUENCE pseudo_sequence START 40","CREATE FUNCTION pseudo_default(a anyelement,b anyelement DEFAULT nextval('pseudo_sequence')) RETURNS anyelement LANGUAGE SQL AS $$SELECT b$$")
		query := "SELECT pseudo_default(7::bigint) AS v"
		statement,err := c.PrepareContext(p.Ctx,query)
		if err != nil { t.Fatal(err) }
		defer statement.Close()
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT last_value,is_called FROM pseudo_sequence",[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(40),false}}))
		for _, last := range []int64{40,41} {
			fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:statement,SQL:query},b.Recorder,fixture.Select(query,[]string{"v"},[]string{"INT8"},[][]any{{last}}))
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT last_value,is_called FROM pseudo_sequence",[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{last,true}}))
		}
	})
}
