package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func resultsFormalBuiltinNames(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	// Full released declarations include input names as well as OUT names.
	// They must align with alltypes, not be truncated to invocation arity.
	want := [][]any{
		{int64(1689), "{acl,grantor,grantee,privilege_type,is_grantable}"},
		{int64(3084), "{name,source,target,path}"},
		{int64(3208), "{from_json,key,value}"},
		{int64(3219), "{from_json,value}"},
		{int64(3382), "{classid,objid,objsubid,type,object_names,object_args}"},
		{int64(3423), "{rootrelid,relid,parentrelid,isleaf,level}"},
		{int64(3425), "{partitionid,relid}"},
		{int64(3465), "{from_json,value}"},
		{int64(3583), "{xid,timestamp,roident}"},
		{int64(3713), "{parser_oid,tokid,alias,description}"},
		{int64(3714), "{parser_name,tokid,alias,description}"},
		{int64(3715), "{parser_oid,txt,tokid,token}"},
		{int64(3716), "{parser_name,txt,tokid,token}"},
		{int64(3819), "{multixid,xid,mode}"},
		{int64(3839), "{classid,objid,objsubid,type,schema,name,identity}"},
		{int64(3932), "{from_json,key,value}"},
		{int64(3954), "{type,object_names,object_args,classid,objid,objsubid}"},
		{int64(3955), "{from_json,value}"},
		{int64(3958), "{from_json,key,value}"},
		{int64(3959), "{from_json,key,value}"},
		{int64(3969), "{from_json,value}"},
		{int64(6159), "{fktable,fkcols,pktable,pkcols,is_array,is_opt}"},
		{int64(6168), "{xid,timestamp,roident}"},
	}
	var ids []string
	for _, row := range want {
		ids = append(ids, fmt.Sprint(row[0]))
	}
	resultsFormalBoth(t, p, b, c, "SELECT oid::bigint AS id,proargnames::text AS names FROM pg_proc WHERE oid IN ("+strings.Join(ids, ",")+") ORDER BY oid", []string{"id", "names"}, []string{"INT8", "TEXT"}, want)
}

func TestReferenceRoutineResultsFormalInstalledAllInput(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsExec(t, p, b, c, `CREATE EXTENSION "uuid-ossp"`, "CREATE EXTENSION citext")
		resultsFormalBoth(t, p, b, c, "SELECT proargnames::text AS names,proallargtypes::text AS alltypes,proargmodes::text AS modes,provariadic::bigint AS variadic FROM pg_proc WHERE oid='uuid_generate_v5(uuid,text)'::regprocedure", []string{"names", "alltypes", "modes", "variadic"}, []string{"TEXT", "TEXT", "TEXT", "INT8"}, [][]any{{"{namespace,name}", nil, nil, int64(0)}})
		resultsFormalBoth(t, p, b, c, "SELECT proargnames::text AS names,proallargtypes::text AS alltypes,proargmodes::text AS modes,proretset AS sets FROM pg_proc WHERE oid='regexp_split_to_table(citext,citext)'::regprocedure", []string{"names", "alltypes", "modes", "sets"}, []string{"TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{"{string,pattern}", nil, nil, true}})
	})
}
