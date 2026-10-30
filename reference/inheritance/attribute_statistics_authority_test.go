package reference_test

import (
 "context"
 "strings"
 "stuntgres/reference/fixture"
)

func attributeStatsAuthorityEdges(s attributeStatsScenario){
 s.t.Helper()
 q:=attributeStatsRestore("a",false,"'null_frac',.625::real")
 s.exec("SET ROLE ast_actor")
 held,err:=s.conn.PrepareContext(s.p.Ctx,q);if err!=nil{s.t.Fatal(err)};defer held.Close()
 s.exec("RESET ROLE");s.exec("REVOKE MAINTAIN ON ast_t FROM ast_actor");s.exec("SET ROLE ast_actor")
 d:=fixture.ControlsReferenceDiagnostic("42501","permission denied for table ast_t")
 fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{s.conn,held,q},s.b.Recorder,fixture.ImplicitError(q,d))
 s.exec("RESET ROLE");s.exec("GRANT MAINTAIN ON ast_t TO ast_actor");s.exec("SET ROLE ast_actor")
 fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{s.conn,held,q},s.b.Recorder,fixture.Select(q,[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.exec("RESET ROLE")
 s.exec("CREATE INDEX ast_idx ON ast_t(a)")
 indexRestore:="SELECT pg_restore_attribute_stats('schemaname','public','relname','ast_idx','attnum',1::smallint,'inherited',false,'null_frac',.375::real)"
 s.exec("SET ROLE ast_actor")
 s.run(fixture.Select(indexRestore,[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.exec("RESET ROLE")
 s.run(fixture.Select("SELECT attname,null_frac FROM pg_stats WHERE schemaname='public' AND tablename='ast_idx'",[]string{"attname","null_frac"},[]string{"NAME","FLOAT4"},[][]any{{"a",float64(.375)}}))
 s.run(fixture.Select("SELECT pg_clear_attribute_stats('public','ast_idx','a',false)",[]string{"pg_clear_attribute_stats"},[]string{"VOID"},[][]any{{""}}))
 s.run(fixture.Select("SELECT attname FROM pg_stats WHERE schemaname='public' AND tablename='ast_idx'",[]string{"attname"},[]string{"NAME"},nil))
 s.scalars([]any{"a",false,float64(.625),int64(0),float64(0)})
 // Database ownership authorizes another role's relation independently of
 // SELECT, MAINTAIN, table ownership, or prepared target decisions.
 var database,owner string
 if err=s.conn.QueryRowContext(s.p.Ctx,"SELECT datname,pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database()").Scan(&database,&owner);err!=nil{s.t.Fatal(err)}
 quote:=func(v string)string{return `"`+strings.ReplaceAll(v,`"`,`""`)+`"`}
 s.exec("CREATE ROLE ast_database_owner")
 restoreOwner:="ALTER DATABASE "+quote(database)+" OWNER TO "+quote(owner)
 databaseOwnerDropped:=false
 s.t.Cleanup(func(){if databaseOwnerDropped{return};for _,q:=range []string{"RESET ROLE",restoreOwner,"REVOKE USAGE ON SCHEMA public FROM ast_database_owner","DROP ROLE IF EXISTS ast_database_owner"}{if _,err:=s.conn.ExecContext(context.Background(),q);err!=nil{s.t.Errorf("database-owner fixture cleanup %s: %v",q,err)}}})
 s.exec("GRANT USAGE ON SCHEMA public TO ast_database_owner")
 s.exec("ALTER DATABASE "+quote(database)+" OWNER TO ast_database_owner")
 s.exec("SET ROLE ast_database_owner");s.restore("'null_frac',.875::real",true);s.scalars();s.clear("a",false)
 s.exec("RESET ROLE");s.exec(restoreOwner);s.exec("REVOKE USAGE ON SCHEMA public FROM ast_database_owner");s.exec("DROP ROLE ast_database_owner");databaseOwnerDropped=true
 s.scalars()
}
