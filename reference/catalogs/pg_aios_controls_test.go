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

func TestReferencePGAIOSNativeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) {
		pgSequenceExec(t, p, b, b.DB, "CREATE TABLE aios_sentinel(n integer)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO aios_sentinel VALUES(42)",1))
		aiosRead(t, p, b, b.DB, true)
		aiosSentinel(t, p, b, b.DB)
		if b.PG { return } // The following controls are public native APIs, not SQL counterparts.
		retained, err := b.DB.QueryContext(p.Ctx, aiosQuery); if err != nil { t.Fatal(err) }; defer retained.Close()
		var baseline bytes.Buffer
		if err := stuntgres.Dump(p.Ctx,p.Native,&baseline); err != nil { t.Fatal(err) }
		if strings.Contains(baseline.String(),"pg_aios") || strings.Contains(baseline.String(),"pg_get_aios") { t.Fatal("default authority or handles serialized",baseline.String()) }
		connector, err := stuntgres.NewDriver().OpenConnector("aios-controls-destination"); if err != nil { t.Fatal(err) }
		destination := sql.OpenDB(connector); defer destination.Close()
		pool, recorder := fixture.RecordedNativePool(t,connector)
		fresh := fixture.Backend{Name:"aios-destination",DB:pool,Recorder:recorder}
		if err := stuntgres.Restore(p.Ctx,destination,strings.NewReader(baseline.String())); err != nil { t.Fatal(err) }
		aiosRead(t,p,fresh,pool,true); aiosCount(t,p,fresh,pool); aiosSentinel(t,p,fresh,pool)
		shmemDefaultACL(t,p,pool,"pg_aios",false)
		aiosAbsentACLControls(t,p,fresh,destination)
		for _, query := range []string{
			"GRANT SELECT ON pg_catalog.pg_aios TO pg_signal_backend",
			"GRANT SELECT(pid) ON pg_catalog.pg_aios TO pg_signal_backend",
			"GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_aios() TO pg_signal_backend",
			"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_aios() FROM pg_read_all_stats",
		} { pgSequenceExec(t,p,b,b.DB,query) }
		var custom bytes.Buffer
		if err := stuntgres.Dump(p.Ctx,p.Native,&custom); err != nil { t.Fatal(err) }
		if err := stuntgres.Restore(p.Ctx,destination,strings.NewReader(custom.String())); err != nil { t.Fatal(err) }
		conn := fixture.SequenceCoreConn(t,p.Ctx,pool)
		defer conn.ExecContext(context.Background(),"RESET ROLE")
		pgSequenceExec(t,p,fresh,conn,"SET ROLE pg_signal_backend")
		aiosRead(t,p,fresh,conn,true)
		pgSequenceExec(t,p,fresh,conn,"RESET ROLE")
		pgSequenceExec(t,p,fresh,conn,"SET ROLE pg_read_all_stats")
		aiosDenied(t,p,fresh,conn,true)
		pgSequenceExec(t,p,fresh,conn,"RESET ROLE")
		for _, tc := range []struct{query,column,acl string}{
			{"SELECT relacl::text FROM pg_class WHERE relname='pg_aios'","relacl","{postgres=arwdDxtm/postgres,pg_read_all_stats=r/postgres,pg_signal_backend=r/postgres}"},
			{"SELECT proacl::text FROM pg_proc WHERE oid=6399","proacl","{postgres=X/postgres,pg_signal_backend=X/postgres}"},
		} { fixture.RunStep(t,p.Ctx,pool,recorder,fixture.Select(tc.query,[]string{tc.column},[]string{"TEXT"},[][]any{{tc.acl}})) }
		fixture.RunStep(t,p.Ctx,pool,recorder,fixture.Select("SELECT attacl::text FROM pg_attribute WHERE attrelid='pg_catalog.pg_aios'::regclass AND attname='pid'",[]string{"attacl"},[]string{"TEXT"},[][]any{{"{pg_signal_backend=r/postgres}"}}))
		if err := stuntgres.Restore(p.Ctx,destination,strings.NewReader(baseline.String()+"not valid SQL;")); err == nil { t.Fatal("invalid restore succeeded") }
		aiosSentinel(t,p,fresh,pool)
		pgSequenceExec(t,p,fresh,conn,"SET ROLE pg_read_all_stats")
		aiosDenied(t,p,fresh,conn,true)
		pgSequenceExec(t,p,fresh,conn,"RESET ROLE")
		// Explicit empty ACLs must be replayed, not mistaken for missing defaults.
		for _, query := range []string{
			"REVOKE ALL ON pg_catalog.pg_aios FROM postgres,pg_read_all_stats,pg_signal_backend",
			"REVOKE ALL ON FUNCTION pg_catalog.pg_get_aios() FROM postgres,pg_read_all_stats,pg_signal_backend",
		} { pgSequenceExec(t,p,b,b.DB,query) }
		var empty bytes.Buffer
		if err := stuntgres.Dump(p.Ctx,p.Native,&empty); err != nil { t.Fatal(err) }
		if err := stuntgres.Restore(p.Ctx,destination,strings.NewReader(empty.String())); err != nil { t.Fatal(err) }
		fixture.RunStep(t,p.Ctx,pool,recorder,fixture.Select("SELECT relacl::text FROM pg_class WHERE relname='pg_aios'",[]string{"relacl"},[]string{"TEXT"},[][]any{{"{}"}}))
		fixture.RunStep(t,p.Ctx,pool,recorder,fixture.Select("SELECT proacl::text FROM pg_proc WHERE oid=6399",[]string{"proacl"},[]string{"TEXT"},[][]any{{"{}"}}))
		pgSequenceExec(t,p,fresh,conn,"SET ROLE pg_read_all_stats")
		aiosDenied(t,p,fresh,conn,false)
		pgSequenceExec(t,p,fresh,conn,"RESET ROLE")
		if err := stuntgres.Reset(p.Ctx,destination); err != nil { t.Fatal(err) }
		shmemDefaultACL(t,p,pool,"pg_aios",false)
		pgSequenceExec(t,p,fresh,conn,"SET ROLE pg_read_all_stats")
		aiosRead(t,p,fresh,conn,false)
		pgSequenceExec(t,p,fresh,conn,"RESET ROLE")
		aiosCount(t,p,fresh,pool)
		if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
		aiosRead(t,p,b,b.DB,true)
		if err := stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(baseline.String())); err != nil { t.Fatal(err) }
		aiosRows(t,retained,false)
		aiosSentinel(t,p,b,b.DB)
		aiosRead(t,p,b,b.DB,false)
	}) }
}

func aiosAbsentACLControls(t *testing.T, p *fixture.Pair, b fixture.Backend, control *sql.DB) {
	t.Helper()
	// This predefined ordinary role has no stats membership or explicit backing
	// EXECUTE grant. NULL proacl supplies PUBLIC EXECUTE, unlike the seeded ACL.
	pgSequenceExec(t,p,b,b.DB,"GRANT SELECT ON pg_catalog.pg_aios TO pg_signal_backend")
	var setup bytes.Buffer
	if err := stuntgres.Dump(p.Ctx,control,&setup); err != nil { t.Fatal(err) }
	const nullRepair = "UPDATE pg_catalog.pg_proc SET proacl=NULL::pg_catalog.aclitem[] WHERE oid='pg_catalog.pg_get_aios()'::pg_catalog.regprocedure;\n"
	if err := stuntgres.Restore(p.Ctx,control,strings.NewReader(setup.String()+nullRepair)); err != nil { t.Fatal(err) }
	conn := fixture.SequenceCoreConn(t,p.Ctx,b.DB)
	defer conn.ExecContext(context.Background(),"RESET ROLE")
	for round := 0; round <= 2; round++ {
		// Check actual authority, not dump text or effective-rights equivalence.
		s := fixture.Select("SELECT proacl IS NULL AS absent FROM pg_catalog.pg_proc WHERE oid=6399",[]string{"absent"},[]string{"BOOL"},[][]any{{true}})
		fixture.RunStep(t,p.Ctx,conn,b.Recorder,s)
		fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Prepared(s))
		s = fixture.Select("SELECT has_table_privilege('pg_signal_backend','pg_catalog.pg_aios','SELECT') AS can_select,has_function_privilege('pg_signal_backend','pg_catalog.pg_get_aios()','EXECUTE') AS can_execute",[]string{"can_select","can_execute"},[]string{"BOOL","BOOL"},[][]any{{true,true}})
		fixture.RunStep(t,p.Ctx,conn,b.Recorder,s)
		pgSequenceExec(t,p,b,conn,"SET ROLE pg_signal_backend")
		aiosRead(t,p,b,conn,false)
		aiosRead(t,p,b,conn,true)
		pgSequenceExec(t,p,b,conn,"RESET ROLE")
		aiosSentinel(t,p,b,conn)
		if round < 2 {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx,control,&dump); err != nil { t.Fatal(err) }
			if err := stuntgres.Restore(p.Ctx,control,strings.NewReader(dump.String())); err != nil { t.Fatal(err) }
		}
	}
}
