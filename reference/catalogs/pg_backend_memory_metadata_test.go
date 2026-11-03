package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func backendMemoryStepRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t,p.Ctx,e,b.Recorder,s)
	fixture.RunStep(t,p.Ctx,e,b.Recorder,fixture.Prepared(s))
	held:=backendMemoryHeld(t,p,e,s.Query)
	fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
}

func backendMemoryCatalogMetadata(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	q:="SELECT c.oid>0 AS valid_identity,c.relkind::text,c.relnamespace::bigint,c.reltype::bigint,t.typarray::bigint,t.typrelid=c.oid AS owns_row,a.typelem=t.oid AS owns_array,c.relnatts::integer,c.relispopulated,c.relrowsecurity,c.relforcerowsecurity,c.reloptions::text FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_backend_memory_contexts'::regclass"
	backendMemoryStepRoutes(t,p,b,c,fixture.Select(q,[]string{"valid_identity","relkind","relnamespace","reltype","typarray","owns_row","owns_array","relnatts","relispopulated","relrowsecurity","relforcerowsecurity","reloptions"},[]string{"BOOL","TEXT","INT8","INT8","INT8","BOOL","BOOL","INT4","BOOL","BOOL","BOOL","TEXT"},[][]any{{true,"v",int64(11),int64(12144),int64(12143),true,true,int64(10),true,false,false,nil}}))
	q="SELECT pg_typeof(m)::oid::bigint AS row_type,pg_typeof(m.path)::oid::bigint AS path_type,pg_typeof(ARRAY[m])::oid::bigint AS array_type FROM pg_catalog.pg_backend_memory_contexts AS m WHERE name='TopMemoryContext'"
	backendMemoryStepRoutes(t,p,b,c,fixture.Select(q,[]string{"row_type","path_type","array_type"},[]string{"INT8","INT8","INT8"},[][]any{{int64(12144),int64(1007),int64(12143)}}))
}

func backendMemoryAttributes(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	var attrs [][]any
	for i,name:=range backendMemoryNames {collation:=int64(0);if backendMemoryTypeIDs[i]==25{collation=100};attrs=append(attrs,[]any{int64(i+1),name,backendMemoryTypeIDs[i],false,int64(-1),int64(0),collation,false,"",""})}
	q:="SELECT attnum::integer,attname::text,atttypid::bigint,attnotnull,atttypmod::integer,attndims::integer,attcollation::bigint,attisdropped,attidentity::text,attgenerated::text FROM pg_attribute WHERE attrelid='pg_catalog.pg_backend_memory_contexts'::regclass AND attnum>0 ORDER BY attnum"
	backendMemoryStepRoutes(t,p,b,c,fixture.Select(q,[]string{"attnum","attname","atttypid","attnotnull","atttypmod","attndims","attcollation","attisdropped","attidentity","attgenerated"},[]string{"INT4","TEXT","INT8","BOOL","INT4","INT4","INT8","BOOL","TEXT","TEXT"},attrs))
}

func backendMemoryRoutineMetadata(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	owner:="postgres";if b.PG{owner="stuntgres"}
	q:=`SELECT p.oid::bigint,p.proname::text,p.pronamespace::bigint,r.rolname::text,p.proacl::text,p.prolang::bigint,p.prorettype::bigint,p.proretset,p.provolatile::text,p.proisstrict,p.proparallel::text,p.proargnames::text,p.proallargtypes::text,p.proargmodes::text,p.prosrc,p.prorows,p.procost,p.pronargs::integer,p.pronargdefaults::integer,p.provariadic::bigint,p.prosupport::oid::bigint,p.prokind::text,p.prosecdef,p.proleakproof,p.proargtypes::text,p.proargdefaults IS NULL AS no_defaults,p.probin,p.proconfig::text,p.protrftypes::text,p.prosqlbody IS NULL AS no_body FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid=2282`
	want:=[][]any{{int64(2282),"pg_get_backend_memory_contexts",int64(11),owner,"{"+owner+"=X/"+owner+",pg_read_all_stats=X/"+owner+"}",int64(12),int64(2249),true,"v",true,"r","{"+strings.Join(backendMemoryNames,",")+"}","{25,25,25,23,1007,20,20,20,20,20}","{o,o,o,o,o,o,o,o,o,o}","pg_get_backend_memory_contexts",float64(100),float64(1),int64(0),int64(0),int64(0),int64(0),"f",false,false,"",true,nil,nil,nil,true}}
	backendMemoryStepRoutes(t,p,b,c,fixture.Select(q,[]string{"oid","proname","pronamespace","rolname","proacl","prolang","prorettype","proretset","provolatile","proisstrict","proparallel","proargnames","proallargtypes","proargmodes","prosrc","prorows","procost","pronargs","pronargdefaults","provariadic","prosupport","prokind","prosecdef","proleakproof","proargtypes","no_defaults","probin","proconfig","protrftypes","no_body"},[]string{"INT8","TEXT","INT8","TEXT","TEXT","INT8","INT8","BOOL","TEXT","BOOL","TEXT","TEXT","TEXT","TEXT","TEXT","FLOAT4","FLOAT4","INT4","INT4","INT8","INT8","TEXT","BOOL","BOOL","TEXT","BOOL","TEXT","TEXT","TEXT","BOOL"},want))
	backendMemoryInitialPrivileges(t,p,b,c)
}

func backendMemoryInfoMetadata(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	dataTypes:=[]string{"text","text","text","integer","ARRAY","bigint","bigint","bigint","bigint","bigint"}
	udts:=[]string{"text","text","text","int4","_int4","int8","int8","int8","int8","int8"}
	var columns,params [][]any
	for i,name:=range backendMemoryNames {
		ordinal:=int64(i+1);id:=strconv.Itoa(i+1)
		columns=append(columns,[]any{name,ordinal,"YES",dataTypes[i],"pg_catalog",udts[i],id,nil})
		params=append(params,[]any{ordinal,"OUT",name,dataTypes[i],"pg_catalog",udts[i],id,nil})
	}
	q:="SELECT column_name::text,ordinal_position::integer,is_nullable::text,data_type::text,udt_schema::text,udt_name::text,dtd_identifier::text,column_default::text FROM information_schema.columns WHERE table_schema='pg_catalog' AND table_name='pg_backend_memory_contexts' ORDER BY ordinal_position"
	backendMemoryStepRoutes(t,p,b,c,fixture.Select(q,[]string{"column_name","ordinal_position","is_nullable","data_type","udt_schema","udt_name","dtd_identifier","column_default"},[]string{"TEXT","INT4","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT"},columns))
	q="SELECT ordinal_position::integer,parameter_mode::text,parameter_name::text,data_type::text,udt_schema::text,udt_name::text,dtd_identifier::text,parameter_default::text FROM information_schema.parameters WHERE specific_schema='pg_catalog' AND specific_name='pg_get_backend_memory_contexts_2282' ORDER BY ordinal_position"
	backendMemoryStepRoutes(t,p,b,c,fixture.Select(q,[]string{"ordinal_position","parameter_mode","parameter_name","data_type","udt_schema","udt_name","dtd_identifier","parameter_default"},[]string{"INT4","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT"},params))
	q="SELECT specific_name::text,routine_name::text,routine_type::text,data_type::text,type_udt_schema::text,type_udt_name::text,routine_definition::text,external_language::text,security_type::text,is_deterministic::text,sql_data_access::text,is_null_call::text FROM information_schema.routines WHERE specific_schema='pg_catalog' AND specific_name='pg_get_backend_memory_contexts_2282'"
	backendMemoryStepRoutes(t,p,b,c,fixture.Select(q,[]string{"specific_name","routine_name","routine_type","data_type","type_udt_schema","type_udt_name","routine_definition","external_language","security_type","is_deterministic","sql_data_access","is_null_call"},[]string{"TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT"},[][]any{{"pg_get_backend_memory_contexts_2282","pg_get_backend_memory_contexts","FUNCTION","record","pg_catalog","record","pg_get_backend_memory_contexts","INTERNAL","INVOKER","NO","MODIFIES","YES"}}))
	q="SELECT object_name::text,object_type::text,collection_type_identifier::text,data_type::text,udt_schema::text,udt_name::text,dtd_identifier::text FROM information_schema.element_types WHERE object_schema='pg_catalog' AND object_name IN ('pg_backend_memory_contexts','pg_get_backend_memory_contexts_2282') ORDER BY object_name,collection_type_identifier"
	backendMemoryStepRoutes(t,p,b,c,fixture.Select(q,[]string{"object_name","object_type","collection_type_identifier","data_type","udt_schema","udt_name","dtd_identifier"},[]string{"TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT"},[][]any{{"pg_backend_memory_contexts","TABLE","5","integer","pg_catalog","int4","a5"},{"pg_get_backend_memory_contexts_2282","ROUTINE","5","integer","pg_catalog","int4","a5"}}))
}

func backendMemoryDefinition(names []string, quote, shadow bool) string {
	fields:=append([]string(nil),names...)
	fn:="pg_get_backend_memory_contexts"
	if quote {fn=`"pg_get_backend_memory_contexts"`;for i:=range fields{fields[i]=`"`+fields[i]+`"`}}
	call:=fn;if shadow {if quote {call=`"pg_catalog".`+fn}else{call="pg_catalog."+fn}}
	return " SELECT "+strings.Join(fields,",\n    ")+"\n   FROM "+call+"() "+fn+"("+strings.Join(fields,", ")+");"
}

func backendMemoryDefinitions(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	defer c.ExecContext(context.Background(),"RESET quote_all_identifiers")
	defer c.ExecContext(context.Background(),"RESET search_path")
	for _,shadow:=range []bool{false,true} {
		if shadow {pgSequenceExec(t,p,b,c,"CREATE FUNCTION public.pg_get_backend_memory_contexts() RETURNS integer LANGUAGE SQL AS 'SELECT 7'");pgSequenceExec(t,p,b,c,"SET search_path=public,pg_catalog")}
		for _,quote:=range []bool{false,true} {
			setting:="off";if quote{setting="on"};pgSequenceExec(t,p,b,c,"SET quote_all_identifiers="+setting)
			definition:=backendMemoryDefinition(backendMemoryNames,quote,shadow)
			for _,pretty:=range []bool{false,true} {
				backendMemoryStepRoutes(t,p,b,c,fixture.Select("SELECT pg_get_viewdef($1::regclass,$2::boolean)",[]string{"pg_get_viewdef"},[]string{"TEXT"},[][]any{{definition}},"pg_catalog.pg_backend_memory_contexts",pretty))
			}
			backendMemoryStepRoutes(t,p,b,c,fixture.Select("SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_backend_memory_contexts'",[]string{"definition"},[]string{"TEXT"},[][]any{{definition}}))
			backendMemoryStepRoutes(t,p,b,c,fixture.Select("SELECT view_definition::text,check_option::text,is_updatable::text,is_insertable_into::text,is_trigger_updatable::text,is_trigger_deletable::text,is_trigger_insertable_into::text FROM information_schema.views WHERE table_schema='pg_catalog' AND table_name='pg_backend_memory_contexts'",[]string{"view_definition","check_option","is_updatable","is_insertable_into","is_trigger_updatable","is_trigger_deletable","is_trigger_insertable_into"},[]string{"TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT"},[][]any{{definition,"NONE","NO","NO","NO","NO","NO"}}))
		}
	}
}

func TestReferenceBackendMemoryMetadata(t *testing.T) {
	t.Run("protocol",backendMemoryProtocol)
	for _, section:=range []struct{name string; run func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn)}{{"relation",backendMemoryCatalogMetadata},{"attributes",backendMemoryAttributes},{"routine",backendMemoryRoutineMetadata},{"information_schema",backendMemoryInfoMetadata},{"definitions",backendMemoryDefinitions},{"descriptors",func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for _,q:=range []string{backendMemoryView,"SELECT * FROM pg_backend_memory_contexts",`SELECT * FROM "pg_catalog"."pg_backend_memory_contexts"`,backendMemoryFunction,"SELECT (pg_catalog.pg_get_backend_memory_contexts()).*"} {
			s:=fixture.Select(q+" LIMIT 0",backendMemoryNames,backendMemoryTypes,nil);view:=strings.Contains(q,"FROM pg_backend_memory_contexts")||strings.Contains(q,"FROM pg_catalog.pg_backend_memory_contexts")||strings.Contains(q,`FROM "pg_catalog"`);s.CheckMetadata=func(t *testing.T,r *sql.Rows){backendMemoryMetadata(t,r,b.PG,view)};backendMemoryStepRoutes(t,p,b,c,s)
		}
		backendMemoryStepRoutes(t,p,b,c,fixture.Select("SELECT pg_catalog.pg_get_backend_memory_contexts() AS context LIMIT 0",[]string{"context"},[]string{"RECORD"},nil))
	}}} {
		t.Run(section.name,func(t *testing.T){p:=fixture.NewScenario(t);for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){c:=fixture.SequenceCoreConn(t,p.Ctx,b.DB);section.run(t,p,b,c)})}})
	}
}
