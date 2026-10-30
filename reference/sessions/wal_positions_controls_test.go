package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceWALPositionsNativeControls(t *testing.T) {
	const role = "wal_positions_restore_reader"
	fixture.RoleReferenceCleanup(t,[]string{role})
	recoveryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
		run(fixture.Exec("CREATE ROLE "+role,0))
		defer walPositionRestore(t,p,c,b.PG,role)()
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role,0))
		run(fixture.Exec("CREATE TABLE wal_positions_control_sentinel(v integer)",0))
		run(fixture.Exec("INSERT INTO wal_positions_control_sentinel VALUES(7)",1))
		run(fixture.Exec("CREATE VIEW wal_positions_control_view AS SELECT pg_create_restore_point(NULL::text) AS marker",0))
		run(fixture.Exec("GRANT SELECT ON wal_positions_control_view TO "+role,0))
		view := fixture.Select("SELECT marker FROM wal_positions_control_view",[]string{"marker"},[]string{"PG_LSN"},[][]any{{nil}})
		replicationMessageRoutes(t,p,b,c,view)
		var baseline bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx,p.Native,&baseline); err != nil { t.Fatal(err) }
			var again bytes.Buffer; if err := stuntgres.Dump(p.Ctx,p.Native,&again); err != nil { t.Fatal(err) }
			if baseline.String()!=again.String() { t.Fatal("unstable untouched dump") }
			for _,id := range walPositionIdentities { if strings.Contains(baseline.String(),"WHERE oid='pg_catalog."+id+"'::pg_catalog.regprocedure") { t.Fatal("gratuitous bootstrap repair",baseline.String()) } }
		}
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_switch_wal() TO "+role,0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_current_wal_lsn() FROM PUBLIC",0))
		if b.PG {
			run(fixture.Exec("UPDATE pg_catalog.pg_proc SET proacl=NULL WHERE oid=3098",1))
			run(fixture.Exec(`UPDATE pg_catalog.pg_proc SET proacl=pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE oid=6305`,1))
		} else {
			var seed bytes.Buffer; if err := stuntgres.Dump(p.Ctx,p.Native,&seed); err != nil { t.Fatal(err) }
			seed.WriteString("\nUPDATE pg_catalog.pg_proc SET proacl=NULL::pg_catalog.aclitem[] WHERE oid='pg_catalog.pg_create_restore_point(text)'::pg_catalog.regprocedure;\n")
			seed.WriteString(`UPDATE pg_catalog.pg_proc SET proacl=pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE oid='pg_catalog.pg_log_standby_snapshot()'::pg_catalog.regprocedure;`+"\n")
			if err := stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(seed.String())); err != nil { t.Fatal(err) }
		}
		check := func(conn *sql.Conn,recorder *fixture.Recorder) {
			run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,conn,recorder,s) }
			run(walPositionInitialPrivileges(b.PG))
			run(fixture.Select("SELECT oid::bigint AS oid,proacl::text AS acl FROM pg_proc WHERE oid IN (2848,2849,3098,6305) ORDER BY oid",[]string{"oid","acl"},[]string{"INT8","TEXT"},[][]any{
				{int64(2848),strings.TrimSuffix(recoveryControlOwnerACL(b.PG),"}")+","+role+"=X/"+func() string { if b.PG { return "stuntgres" }; return "postgres" }()+"}"},
				{int64(2849),recoveryControlOwnerACL(b.PG)},{int64(3098),nil},{int64(6305),"{}"},
			}))
			run(fixture.Select("SELECT v FROM wal_positions_control_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role,0))
			run(view)
			for i,call := range walPositionCalls {
				allowed := i!=2 && i!=5
				run(fixture.Select("SELECT has_function_privilege(current_user,'"+walPositionIdentities[i]+"','EXECUTE') AS allowed",[]string{"allowed"},[]string{"BOOL"},[][]any{{allowed}}))
				q := "SELECT "+call
				if allowed { bb:=b; bb.Recorder=recorder; walPositionBoundary(t,p,bb,conn,q,walPositionNames[i]) } else { run(recoveryControlDenied(q,walPositionNames[i])) }
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		}
		check(c,b.Recorder)
		if b.PG { return } // No PostgreSQL counterpart to native catalog replacement.
		var dump bytes.Buffer; if err := stuntgres.Dump(p.Ctx,p.Native,&dump); err != nil { t.Fatal(err) }
		retained,err := c.QueryContext(p.Ctx,view.Query); if err != nil { t.Fatal(err) }; defer retained.Close()
		metadata,err := retained.ColumnTypes(); if err != nil { t.Fatal(err) }
		if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
		for i,identity := range walPositionIdentities {
			var acl sql.NullString
			if err := c.QueryRowContext(p.Ctx,"SELECT proacl::text FROM pg_proc WHERE oid='"+identity+"'::regprocedure").Scan(&acl); err != nil { t.Fatal(err) }
			if i<3 { if acl.Valid { t.Fatal("Reset read raw ACL",acl) } } else if !acl.Valid || acl.String!=recoveryControlOwnerACL(false) { t.Fatal("Reset restricted seed",acl) }
		}
		run(walPositionInitialPrivileges(false))
		if err := stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump.String())); err != nil { t.Fatal(err) }
		check(c,b.Recorder)
		if len(metadata)!=1 || metadata[0].DatabaseTypeName()!="PG_LSN" || !retained.Next() { t.Fatal("retained typed NULL metadata",metadata,retained.Err()) }
		var saved any; if err := retained.Scan(&saved); err != nil || saved!=nil { t.Fatal("retained NULL",saved,err) }; retained.Close()
		var redump bytes.Buffer; if err := stuntgres.Dump(p.Ctx,p.Native,&redump); err != nil { t.Fatal(err) }
		if dump.String()!=redump.String() { t.Fatal("noncanonical ACL/view replay",dump.String(),redump.String()) }
		bad := dump.String()+"\nINSERT INTO wal_positions_control_sentinel VALUES(99);\nSELECT no_wal_restore_function();\n"
		if err := stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(bad)); err==nil { t.Fatal("accepted failed Restore") }
		check(c,b.Recorder)
		var after bytes.Buffer; if err := stuntgres.Dump(p.Ctx,p.Native,&after); err != nil { t.Fatal(err) }
		if redump.String()!=after.String() { t.Fatal("failed Restore changed committed catalog") }
		connector,err := stuntgres.NewDriver().OpenConnector("wal_positions_destination")
		if err != nil { t.Fatal(err) }
		control := sql.OpenDB(connector); defer control.Close()
		recorded,recorder := fixture.RecordedNativePool(t,connector)
		if _,err := control.ExecContext(p.Ctx,"CREATE ROLE "+role); err != nil { t.Fatal(err) }
		if err := stuntgres.Restore(p.Ctx,control,strings.NewReader(dump.String())); err != nil { t.Fatal(err) }
		destination,err := recorded.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer destination.Close()
		check(destination,recorder)
		var imported bytes.Buffer; if err := stuntgres.Dump(p.Ctx,control,&imported); err != nil { t.Fatal(err) }
		if imported.String()!=dump.String() { t.Fatal("destination changed logical ACL identity",imported.String(),dump.String()) }
	})
}
