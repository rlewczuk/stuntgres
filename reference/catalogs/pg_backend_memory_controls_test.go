package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func backendMemoryDump(t *testing.T,p *fixture.Pair,control *sql.DB) string {
	t.Helper()
	var dump bytes.Buffer
	if err:=stuntgres.Dump(p.Ctx,control,&dump);err!=nil{t.Fatal(err)}
	return dump.String()
}

func backendMemoryRestore(t *testing.T,p *fixture.Pair,control *sql.DB,dump string) {
	t.Helper()
	if err:=stuntgres.Restore(p.Ctx,control,strings.NewReader(dump));err!=nil{t.Fatal(err)}
}

func backendMemoryControlACL(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,relation,routine,column any) {
	t.Helper()
	for _,s:=range []fixture.Step{
		fixture.Select("SELECT relacl::text FROM pg_class WHERE oid='pg_catalog.pg_backend_memory_contexts'::regclass",[]string{"relacl"},[]string{"TEXT"},[][]any{{relation}}),
		fixture.Select("SELECT proacl::text FROM pg_proc WHERE oid=2282",[]string{"proacl"},[]string{"TEXT"},[][]any{{routine}}),
		fixture.Select("SELECT attacl::text FROM pg_attribute WHERE attrelid='pg_catalog.pg_backend_memory_contexts'::regclass AND attname='name'",[]string{"attacl"},[]string{"TEXT"},[][]any{{column}}),
	}{fixture.RunStep(t,p.Ctx,e,b.Recorder,s);fixture.RunStep(t,p.Ctx,e,b.Recorder,fixture.Prepared(s))}
}

func backendMemoryControlDefault(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor) {
	t.Helper()
	backendMemoryControlACL(t,p,b,e,"{postgres=arwdDxtm/postgres,pg_read_all_stats=r/postgres}","{postgres=X/postgres,pg_read_all_stats=X/postgres}",nil)
	backendMemoryInitialPrivileges(t,p,b,e)
}

func backendMemoryControlSmoke(t *testing.T,p *fixture.Pair,b fixture.Backend,owner *sql.Conn) {
	t.Helper()
	backendMemoryTemporarySetup(t,p,b,owner)
	saved,root:=backendMemoryTemporaryCapture(t,p,b,owner)
	held:=backendMemoryHeld(t,p,owner,saved.Query)
	backendMemoryTemporaryStored(t,p,b,owner,held,saved)
	if b.PG{return}
	// The same physical owner and populated TEMP consumer stay live through
	// the existing edited-authority replacement, not a separate smoke scenario.
	dump:=backendMemoryDump(t,p,p.Native)
	backendMemoryRestore(t,p,p.Native,dump)
	backendMemoryTemporaryStored(t,p,b,owner,held,saved)
	backendMemorySentinel(t,p,b,owner)
	for _,source:=range []struct{query string; prepared bool}{{backendMemoryView,false},{backendMemoryFunction,true}} {
		fresh:=backendMemoryRead(t,p,b,owner,source.query,source.prepared)
		if backendMemoryOwn(fresh)["TopMemoryContext"]!=root.Total.Int64 {
			t.Fatal("replacement changed the still-live physical session allocation",fresh,root)
		}
	}
	backendMemoryPrivilegeFlags(t,p,b,owner,true,false)
	backendMemoryInitialPrivileges(t,p,b,owner)
}

func TestReferenceBackendMemoryControls(t *testing.T) {
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
		a,z:=fixture.LockConnections(t,p.Ctx,b.DB)
		backendMemorySetupSentinel(t,p,b,z)
		backendMemoryRead(t,p,b,a,backendMemoryView,false)
		backendMemoryRead(t,p,b,a,backendMemoryFunction,true)
		backendMemorySentinel(t,p,b,z)
		if !b.PG {
			baselineDump:=backendMemoryDump(t,p,p.Native)
			if strings.Contains(baselineDump,"pg_backend_memory_contexts")||strings.Contains(baselineDump,"pg_get_backend_memory_contexts")||strings.Contains(baselineDump,"GoObjectLayout"){t.Fatal("default authority/live observations serialized",baselineDump)}
			pgSequenceExec(t,p,b,a,"PREPARE backend_memory_retained AS SELECT 42 AS n")
			owned:=backendMemoryRead(t,p,b,a,backendMemoryView,false)
			retained,err:=a.QueryContext(p.Ctx,backendMemoryView);if err!=nil{t.Fatal(err)};defer retained.Close()
			metadata,err:=retained.ColumnTypes();if err!=nil{t.Fatal(err)}
			held:=backendMemoryHeld(t,p,a,backendMemoryFunction)
			if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
			backendMemoryControlDefault(t,p,b,z)
			backendMemorySameOwn(t,backendMemoryRead(t,p,b,a,backendMemoryView,false),owned)
			backendMemoryNamesInstalled(t,p,b,a,"backend_memory_retained")
			fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("EXECUTE backend_memory_retained",[]string{"n"},[]string{"INT4"},[][]any{{int64(42)}}))
			backendMemoryRestore(t,p,p.Native,baselineDump)
			backendMemoryNamesInstalled(t,p,b,a,"backend_memory_retained")
			fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("EXECUTE backend_memory_retained",[]string{"n"},[]string{"INT4"},[][]any{{int64(42)}}))
			backendMemorySameOwn(t,backendMemoryRead(t,p,b,held,backendMemoryFunction,false),owned)
			backendMemoryRetainedMetadata(t,retained,metadata)
			backendMemorySameOwn(t,backendMemoryConsume(t,retained,false,true),owned)
			backendMemorySentinel(t,p,b,z)
			connector,err:=stuntgres.NewDriver().OpenConnector("backend-memory-controls-destination");if err!=nil{t.Fatal(err)}
			destination:=sql.OpenDB(connector);defer destination.Close()
			pool,recorder:=fixture.RecordedNativePool(t,connector)
			fresh:=fixture.Backend{Name:"backend-memory-destination",DB:pool,Recorder:recorder}
			backendMemoryRestore(t,p,destination,baselineDump)
			c:=fixture.SequenceCoreConn(t,p.Ctx,pool)
			defer c.ExecContext(context.Background(),"RESET ROLE")
			backendMemoryNamesInstalled(t,p,fresh,c)
			rows:=backendMemoryRead(t,p,fresh,c,backendMemoryView,false)
			if backendMemoryOwn(rows)["PreparedDefinitions"]!=0{t.Fatal("source definitions imported",rows)}
			backendMemoryRead(t,p,fresh,c,backendMemoryFunction,true)
			backendMemoryStepRoutes(t,p,fresh,c,fixture.Select("SELECT pg_typeof(m)::oid::bigint AS row_type,pg_typeof(m.path)::oid::bigint AS path_type FROM pg_catalog.pg_backend_memory_contexts AS m WHERE name='TopMemoryContext'",[]string{"row_type","path_type"},[]string{"INT8","INT8"},[][]any{{int64(12144),int64(1007)}}))
			backendMemorySentinel(t,p,fresh,c)
			backendMemoryControlDefault(t,p,fresh,c)
			for _,q:=range []string{
				"GRANT SELECT ON backend_memory_sentinel TO pg_signal_backend",
				"GRANT SELECT ON pg_catalog.pg_backend_memory_contexts TO pg_signal_backend",
				"GRANT SELECT(name) ON pg_catalog.pg_backend_memory_contexts TO pg_signal_backend",
				"GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_backend_memory_contexts() TO pg_signal_backend",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_backend_memory_contexts() FROM pg_read_all_stats",
			}{pgSequenceExec(t,p,b,z,q)}
			custom:=backendMemoryDump(t,p,p.Native)
			if strings.Contains(custom,"GoObjectLayout")||strings.Contains(custom,"PREPARE backend_memory_retained"){t.Fatal("session owners serialized",custom)}
			for range 2 {
				backendMemoryRestore(t,p,destination,custom)
				backendMemoryControlACL(t,p,fresh,c,"{postgres=arwdDxtm/postgres,pg_read_all_stats=r/postgres,pg_signal_backend=r/postgres}","{postgres=X/postgres,pg_signal_backend=X/postgres}","{pg_signal_backend=r/postgres}")
				backendMemoryInitialPrivileges(t,p,fresh,c)
				fixture.RunStep(t,p.Ctx,c,recorder,fixture.Select("SELECT relacl::text FROM pg_class WHERE oid='backend_memory_sentinel'::regclass",[]string{"relacl"},[]string{"TEXT"},[][]any{{"{postgres=arwdDxtm/postgres,pg_signal_backend=r/postgres}"}}))
				pgSequenceExec(t,p,fresh,c,"SET ROLE pg_signal_backend")
				backendMemoryRead(t,p,fresh,c,backendMemoryView,false);backendMemoryRead(t,p,fresh,c,backendMemoryFunction,true)
				backendMemorySentinel(t,p,fresh,c)
				pgSequenceExec(t,p,fresh,c,"RESET ROLE")
				pgSequenceExec(t,p,fresh,c,"SET ROLE pg_read_all_stats")
				backendMemoryDenied(t,p,fresh,c,backendMemoryView,true);backendMemoryDenied(t,p,fresh,c,backendMemoryFunction,true)
				pgSequenceExec(t,p,fresh,c,"RESET ROLE")
				backendMemorySentinel(t,p,fresh,c)
				custom=backendMemoryDump(t,p,destination)
			}
			pgSequenceExec(t,p,fresh,c,"PREPARE backend_memory_destination AS SELECT 42 AS n")
			beforeFailure:=backendMemoryRead(t,p,fresh,c,backendMemoryView,false)
			bad:=baselineDump+"REVOKE ALL ON pg_catalog.pg_backend_memory_contexts FROM pg_read_all_stats;\nnot valid SQL;\n"
			if err:=stuntgres.Restore(p.Ctx,destination,strings.NewReader(bad));err==nil{t.Fatal("invalid staged restore succeeded")}
			backendMemoryControlACL(t,p,fresh,c,"{postgres=arwdDxtm/postgres,pg_read_all_stats=r/postgres,pg_signal_backend=r/postgres}","{postgres=X/postgres,pg_signal_backend=X/postgres}","{pg_signal_backend=r/postgres}")
			backendMemorySameOwn(t,backendMemoryRead(t,p,fresh,c,backendMemoryView,false),beforeFailure)
			backendMemorySentinel(t,p,fresh,c)
			backendMemoryNamesInstalled(t,p,fresh,c,"backend_memory_destination")
			fixture.RunStep(t,p.Ctx,c,recorder,fixture.Select("EXECUTE backend_memory_destination",[]string{"n"},[]string{"INT4"},[][]any{{int64(42)}}))
			pgSequenceExec(t,p,fresh,c,"DEALLOCATE backend_memory_destination")
			// NULL means SQL's ordinary ACL default, not the restricted seed.
			nullRepair:="UPDATE pg_catalog.pg_proc SET proacl=NULL::pg_catalog.aclitem[] WHERE oid='pg_catalog.pg_get_backend_memory_contexts()'::pg_catalog.regprocedure;\nUPDATE pg_catalog.pg_class SET relacl=NULL::pg_catalog.aclitem[] WHERE oid='pg_catalog.pg_backend_memory_contexts'::pg_catalog.regclass;\n"
			backendMemoryRestore(t,p,destination,custom+nullRepair)
			for range 3 {
				backendMemoryControlACL(t,p,fresh,c,nil,nil,"{pg_signal_backend=r/postgres}")
				fixture.RunStep(t,p.Ctx,c,recorder,fixture.Select("SELECT has_table_privilege('pg_signal_backend','pg_catalog.pg_backend_memory_contexts','SELECT') AS can_select,has_function_privilege('pg_signal_backend','pg_catalog.pg_get_backend_memory_contexts()','EXECUTE') AS can_execute",[]string{"can_select","can_execute"},[]string{"BOOL","BOOL"},[][]any{{false,true}}))
				pgSequenceExec(t,p,fresh,c,"SET ROLE pg_signal_backend")
				backendMemoryRead(t,p,fresh,c,backendMemoryFunction,false)
				backendMemoryStepRoutes(t,p,fresh,c,fixture.Select("SELECT name FROM pg_catalog.pg_backend_memory_contexts ORDER BY name",[]string{"name"},[]string{"TEXT"},[][]any{{"PreparedDefinitions"},{"PreparedStatements"},{"TopMemoryContext"}}))
				backendMemoryDenied(t,p,fresh,c,backendMemoryView,false)
				pgSequenceExec(t,p,fresh,c,"RESET ROLE")
				backendMemoryInitialPrivileges(t,p,fresh,c)
				dump:=backendMemoryDump(t,p,destination);backendMemoryRestore(t,p,destination,dump)
			}
			backendMemoryRestore(t,p,destination,custom)
			for _,q:=range []string{
				"REVOKE ALL ON pg_catalog.pg_backend_memory_contexts FROM postgres,pg_read_all_stats,pg_signal_backend",
				"REVOKE SELECT(name) ON pg_catalog.pg_backend_memory_contexts FROM pg_signal_backend",
				"REVOKE ALL ON FUNCTION pg_catalog.pg_get_backend_memory_contexts() FROM postgres,pg_read_all_stats,pg_signal_backend",
			}{pgSequenceExec(t,p,fresh,c,q)}
			for range 2 {
				dump:=backendMemoryDump(t,p,destination);backendMemoryRestore(t,p,destination,dump)
				backendMemoryControlACL(t,p,fresh,c,"{}","{}",nil)
				pgSequenceExec(t,p,fresh,c,"SET ROLE pg_read_all_stats")
				backendMemoryDenied(t,p,fresh,c,backendMemoryView,false);backendMemoryDenied(t,p,fresh,c,backendMemoryFunction,true)
				pgSequenceExec(t,p,fresh,c,"RESET ROLE")
				backendMemoryInitialPrivileges(t,p,fresh,c)
			}
			if err:=stuntgres.Reset(p.Ctx,destination);err!=nil{t.Fatal(err)}
			backendMemoryControlDefault(t,p,fresh,c)
			backendMemoryRead(t,p,fresh,c,backendMemoryView,false)
			backendMemoryRestore(t,p,p.Native,baselineDump)
			backendMemoryControlDefault(t,p,b,z)
			backendMemorySameOwn(t,backendMemoryRead(t,p,b,a,backendMemoryFunction,false),owned)
			pgSequenceExec(t,p,b,a,"DEALLOCATE backend_memory_retained")
			backendMemorySentinel(t,p,b,z)
		}
	})}
}
