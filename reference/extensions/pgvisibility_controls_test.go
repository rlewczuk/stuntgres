package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"pv_control_owner","pv_control_reader"})
	pvCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for _,q:=range []string{
			"CREATE ROLE pv_control_owner", "CREATE ROLE pv_control_reader",
			"GRANT USAGE ON SCHEMA pv_part TO pv_control_owner,pv_control_reader",
			"ALTER TABLE pv_part.heap OWNER TO pv_control_owner",
			"GRANT pg_stat_scan_tables TO pv_control_reader",
			"CREATE VIEW pv_part.scalar_view AS SELECT (pv_part.pg_visibility_map_summary('pv_part.heap'::regclass)).all_visible AS n",
			"CREATE VIEW pv_part.source_view AS SELECT * FROM pv_part.pg_visibility('pv_part.heap'::regclass)",
			"CREATE VIEW pv_part.map_view AS SELECT * FROM pv_part.pg_visibility_map('pv_part.heap'::regclass)",
			"CREATE VIEW pv_part.page_view AS SELECT * FROM pv_part.pg_visibility('pv_part.heap'::regclass,0)",
			"CREATE VIEW pv_part.map_page_view AS SELECT * FROM pv_part.pg_visibility_map('pv_part.heap'::regclass,0)",
			"CREATE TABLE pv_part.consumer(v bigint DEFAULT (pv_part.pg_visibility_map_summary('pv_part.heap'::regclass)).all_visible,CHECK(v>=0 AND (pv_part.pg_visibility_map_summary('pv_part.heap'::regclass)).all_visible>=0))",
			"ALTER FUNCTION pv_part.pg_visibility_map_summary(regclass) OWNER TO pv_control_owner",
		} {pvRun(t,p,b,c,fixture.Exec(q,0))}
		pvRun(t,p,b,c,fixture.Exec("INSERT INTO pv_part.heap VALUES(1)",1)); pvFreeze(t,p,b,c)
		post:=func(target *sql.Conn,rec *fixture.Recorder){
			run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,target,rec,s);fixture.RunStep(t,p.Ctx,target,rec,fixture.Prepared(s))}
			run(fixture.Select("SELECT n FROM pv_part.scalar_view",[]string{"n"},[]string{"INT8"},[][]any{{int64(1)}}))
			run(fixture.Select("SELECT * FROM pv_part.source_view ORDER BY blkno",pvPageRowsNames,pvPageRowsTypes,[][]any{{int64(0),true,true,true}}))
			run(fixture.Select("SELECT * FROM pv_part.map_view ORDER BY blkno",pvMapRowsNames,pvMapRowsTypes,[][]any{{int64(0),true,true}}))
			run(fixture.Select("SELECT * FROM pv_part.page_view",pvPageNames,pvPageTypes,[][]any{{true,true,true}}))
			run(fixture.Select("SELECT * FROM pv_part.map_page_view",pvMapNames,pvMapTypes,[][]any{{true,true}}))
			run(fixture.Select("SELECT k FROM pv_part.heap",[]string{"k"},[]string{"INT4"},[][]any{{int64(1)}}))
			run(fixture.Select("SELECT pg_get_userbyid(relowner) AS owner FROM pg_class WHERE oid='pv_part.heap'::regclass",[]string{"owner"},[]string{"NAME"},[][]any{{"pv_control_owner"}}))
			run(fixture.Select("SELECT pg_get_userbyid(proowner) AS owner FROM pg_proc WHERE oid='pv_part.pg_visibility_map_summary(regclass)'::regprocedure",[]string{"owner"},[]string{"NAME"},[][]any{{"pv_control_owner"}}))
			fixture.RunStep(t,p.Ctx,target,rec,fixture.Exec("SET SESSION AUTHORIZATION pv_control_reader",0))
			run(fixture.Select("SELECT * FROM pv_part.pg_visibility_map_summary('pv_part.heap'::regclass)",pvMapNames,pvSummaryTypes,[][]any{{int64(1),int64(1)}}))
			fixture.RunStep(t,p.Ctx,target,rec,pvError("SELECT k FROM pv_part.heap","42501","permission denied for table heap"))
			fixture.RunStep(t,p.Ctx,target,rec,fixture.Exec("RESET SESSION AUTHORIZATION",0))
		}
		defer c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
		post(c,b.Recorder)
		pvRun(t,p,b,c,fixture.Exec("INSERT INTO pv_part.consumer DEFAULT VALUES",1))
		pvBoth(t,p,b,c,fixture.Select("SELECT v FROM pv_part.consumer",[]string{"v"},[]string{"INT8"},[][]any{{int64(1)}}))
		if b.PG {return}
		q:="SELECT * FROM pv_part.pg_visibility($1::regclass,$2::bigint)"
		st,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer st.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:q}
		readHeld:=func(){fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,pvPageNames,pvPageTypes,[][]any{{true,true,true}},"pv_part.heap",int64(0)))}
		readHeld()
		old,err:=c.QueryContext(p.Ctx,"SELECT * FROM pv_part.source_view ORDER BY blkno");if err!=nil {t.Fatal(err)};defer old.Close()
		metadata,err:=old.ColumnTypes();if err!=nil {t.Fatal(err)}
		peer:=pvConn(t,p,b);defer peer.Close();defer pvRollback(peer)
		pvRun(t,p,b,peer,fixture.Exec("BEGIN",0));pvRun(t,p,b,peer,fixture.Exec("INSERT INTO pv_part.heap VALUES(99)",1))
		dump:=fixture.ExtensionDump(t,p.Ctx,p.Native)
		pvRun(t,p,b,peer,fixture.Exec("ROLLBACK",0))
		if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
		if !old.Next(){t.Fatal("retained rows lost",old.Err())}
		var block int64;var av,af,pd bool
		if err:=old.Scan(&block,&av,&af,&pd);err!=nil||block!=0||!av||!af||!pd {t.Fatal("retained flags",block,av,af,pd,err)}
		if old.Next()||old.Err()!=nil {t.Fatal("retained tail",old.Err())};old.Close()
		if len(metadata)!=4||metadata[0].DatabaseTypeName()!="INT8"||metadata[3].DatabaseTypeName()!="BOOL" {t.Fatal("retained metadata",metadata)}
		if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump));err!=nil {t.Fatal(err)}
		// ImportFrozen is genuine fresh provenance. Check current coherence and
		// working identities, not equality to historical physical allocation.
		post(c,b.Recorder);readHeld();pvCoherent(t,p,b,c,"pv_part.heap")
		before:=fixture.ExtensionDump(t,p.Ctx,p.Native)
		for _,bad:=range []string{
			dump+"\nINVALID SQL;",
			dump+"\nCREATE EXTENSION nonexistent_provider;",
			dump+"\nALTER TABLE pv_part.heap OWNER TO pv_missing_role;",
			strings.Replace(dump,"stuntgres-extension-owners-v3","stuntgres-extension-owners-v999",1),
		} {
			if bad==dump {t.Fatal("corrupt fixture unchanged")}
			if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(bad));err==nil {t.Fatal("invalid restore accepted")}
			if fixture.ExtensionDump(t,p.Ctx,p.Native)!=before {t.Fatal("rejected restore published prefix")}
			post(c,b.Recorder);readHeld()
		}
		ctx,cancel:=context.WithCancel(p.Ctx)
		err=stuntgres.Restore(ctx,p.Native,&fixture.CatalogControlCancelReader{Cancel:cancel});cancel()
		if !errors.Is(err,context.Canceled){t.Fatal("canceled restore",err)}
		if fixture.ExtensionDump(t,p.Ctx,p.Native)!=before {t.Fatal("canceled restore published prefix")}
		post(c,b.Recorder);readHeld()
		other,dest,rec:=pgcryptoControlDestination(t,p,"pv_destination")
		defer dest.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
		fixture.RunStep(t,p.Ctx,dest,rec,fixture.Exec("CREATE TABLE destination_collision(k integer)",0))
		fixture.RunStep(t,p.Ctx,dest,rec,fixture.Exec("CREATE TEMP TABLE pv_keep(k integer)",0))
		fixture.RunStep(t,p.Ctx,dest,rec,fixture.Exec("INSERT INTO pv_keep VALUES(19)",1))
		destinationBefore:=fixture.ExtensionDump(t,p.Ctx,other)
		// Named destination authority must not be invented from source OIDs.
		if err:=stuntgres.Restore(p.Ctx,other,strings.NewReader(dump));err==nil {t.Fatal("restore invented missing destination owners")}
		if fixture.ExtensionDump(t,p.Ctx,other)!=destinationBefore {t.Fatal("missing-owner restore published prefix")}
		for _,q:=range []string{"CREATE ROLE pv_control_owner","CREATE ROLE pv_control_reader","GRANT pg_stat_scan_tables TO pv_control_reader"} {fixture.RunStep(t,p.Ctx,dest,rec,fixture.Exec(q,0))}
		if err:=stuntgres.Restore(p.Ctx,other,strings.NewReader(dump));err!=nil {t.Fatal(err)}
		post(dest,rec)
		fixture.RunStep(t,p.Ctx,dest,rec,fixture.Select("SELECT k FROM pv_keep",[]string{"k"},[]string{"INT4"},[][]any{{int64(19)}}))
		fixture.RunStep(t,p.Ctx,dest,rec,fixture.Exec("TRUNCATE pv_part.heap",0))
		fixture.RunStep(t,p.Ctx,dest,rec,fixture.Select("SELECT * FROM pv_part.pg_visibility_map_summary('pv_part.heap'::regclass)",pvMapNames,pvSummaryTypes,[][]any{{int64(0),int64(0)}}))
		post(c,b.Recorder);readHeld()
	})
}
