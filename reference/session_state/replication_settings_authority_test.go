package reference_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func replicationSetDenied() fixture.Diagnostic {
	return physicalDiagnostic("42501",`permission denied to set parameter "output_plugin_libraries"`)
}
func (s *physicalCase) replicationPrepareReaders(show bool) []physicalHeldReader {
	s.t.Helper()
	readers := []physicalHeldReader{{query:"SHOW ALL",kind:"all"},{query:"SELECT name,setting FROM pg_settings WHERE name IN ("+replicationNames()+") ORDER BY name",kind:"catalog"}}
	for _, name := range []string{"primary_conninfo","output_plugin_libraries"} {
		if show { readers = append(readers,physicalHeldReader{query:"SHOW "+name,name:name,kind:"show"}) }
		for _, suffix := range []string{"",",true"} { readers = append(readers,physicalHeldReader{query:"SELECT current_setting('"+name+"'"+suffix+")",name:name,kind:"current"}) }
	}
	for i := range readers {
		stmt,err := s.conn.PrepareContext(s.p.Ctx,readers[i].query); if err != nil { s.t.Fatal(err) }
		readers[i].stmt = stmt; s.t.Cleanup(func(){ stmt.Close() })
	}
	return readers
}
func (s *physicalCase) replicationHeldReads(readers []physicalHeldReader, allowed bool) {
	s.t.Helper()
	for _, r := range readers {
		if r.kind == "all" {
			rows,err := r.stmt.QueryContext(s.p.Ctx); if err != nil { s.t.Fatal(err) }
			replicationShowAllMetadata(s.t,rows,s.pg)
			seen := map[string]bool{}
			for rows.Next() {
				var name,value,description string; if err := rows.Scan(&name,&value,&description); err != nil { s.t.Fatal(err) }
				for _, d := range replicationSettings { if name == d.name {
					if seen[name] || (!allowed && replicationRestricted(name)) || value != d.initial || description != d.description { s.t.Fatal("held SHOW ALL",name,value,description) }; seen[name] = true
				} }
			}
			count := 24; if allowed { count = 26 }
			if rows.Err() != nil || len(seen) != count { s.t.Fatal("held inventory",len(seen),count,rows.Err()) }; rows.Close(); continue
		}
		var step fixture.Step
		if r.kind == "catalog" {
			var want [][]any
			for _, d := range replicationSettings { if allowed || !replicationRestricted(d.name) { want = append(want,[]any{d.name,d.raw}) } }
			step = fixture.Select(r.query,[]string{"name","setting"},[]string{"TEXT","TEXT"},want)
		} else if !allowed {
			step = physicalError(r.query,physicalDenied(r.name),false,true)
		} else {
			column,value := "current_setting",""; if r.kind == "show" { column = r.name }; if r.name == "output_plugin_libraries" { value = "pgoutput, test_decoding" }
			step = fixture.Select(r.query,[]string{column},[]string{"TEXT"},[][]any{{value}})
		}
		fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:r.stmt,SQL:r.query},s.recorder,step)
	}
}

func TestReferenceReplicationSettingsAuthority(t *testing.T) {
	roles := []string{"replication_plain","replication_reader","replication_bridge","replication_inherited","replication_monitor","replication_noinherit","replication_setter","replication_setinherited","replication_super"}
	physicalCases(t,roles,func(s *physicalCase) {
		peer := s.replicationMutableSetup()
		s.run(fixture.Exec("CREATE TABLE replication_authority_outer(v integer)",0))
		for _, role := range roles {
			attributes := " LOGIN PASSWORD 'role-core-fixture'"; if role == "replication_super" { attributes += " SUPERUSER" }
			s.run(fixture.Exec("CREATE ROLE "+role+attributes,0))
		}
		for _, q := range []string{
			"GRANT pg_read_all_settings TO replication_reader",
			"GRANT pg_read_all_settings TO replication_bridge",
			"GRANT replication_bridge TO replication_inherited",
			"GRANT pg_monitor TO replication_monitor",
			"GRANT replication_reader TO replication_noinherit WITH INHERIT FALSE, SET TRUE",
			"GRANT SET ON PARAMETER output_plugin_libraries TO replication_setter",
			"GRANT replication_setter TO replication_setinherited",
		} { s.run(fixture.Exec(q,0)) }
		defer func(){ ctx,cancel := context.WithTimeout(context.Background(),5*time.Second); defer cancel(); if _,err := s.conn.ExecContext(ctx,"REVOKE SET ON PARAMETER output_plugin_libraries FROM replication_setter"); err != nil { s.t.Error(err) } }()
		for _, tc := range []struct{ role string; read,set bool }{
			{"replication_plain",false,false},{"replication_reader",true,false},{"replication_inherited",true,false},{"replication_monitor",true,false},{"replication_noinherit",false,false},{"replication_setter",false,true},{"replication_setinherited",false,true},{"replication_super",true,true},
		} {
			user := s.login(tc.role)
			user.run(fixture.Select("SELECT session_user,current_user",[]string{"session_user","current_user"},[]string{"NAME","NAME"},[][]any{{tc.role,tc.role}}))
			for _, prepared := range []bool{false,true} {
				user.replicationReads(tc.read,prepared)
				if tc.set {
					if tc.read {
						user.replicationSetConfig("output_plugin_libraries",`not-a-real-library, "odd name"`,false,`not-a-real-library, "odd name"`,prepared)
					} else {
						s.replicationSetOnly(user,peer,tc.role,prepared)
					}
					step := fixture.Exec("RESET output_plugin_libraries",0); step.Prepared = prepared; user.run(step)
				} else {
					for _, q := range []string{"SET output_plugin_libraries='anything'","SET output_plugin_libraries=DEFAULT","RESET output_plugin_libraries","SELECT set_config('output_plugin_libraries','anything',false)","SELECT set_config('output_plugin_libraries',NULL,true)"} { user.run(physicalError(q,replicationSetDenied(),prepared,strings.HasPrefix(q,"SELECT"))) }
				}
				if !tc.read { for _, name := range []string{"PRIMARY_CONNINFO","OUTPUT_PLUGIN_LIBRARIES"} {
					step := physicalError("SHOW \""+name+"\"",physicalDenied(name),prepared,true); if prepared { step.ErrorStage = "prepare" }; user.run(step)
					user.run(physicalError("SELECT current_setting($1,true)",physicalDenied(name),prepared,true,name))
				} }
			}
			if tc.role == "replication_noinherit" {
				readers := user.replicationPrepareReaders(false); user.replicationHeldReads(readers,false)
				user.run(fixture.Exec("SET ROLE replication_reader",0)); user.replicationHeldReads(readers,true)
				user.run(fixture.Exec("RESET ROLE",0)); user.replicationHeldReads(readers,false)
			}
			user.conn.Close()
		}
		plain := s.login("replication_plain")
		readers := plain.replicationPrepareReaders(false); plain.replicationHeldReads(readers,false)
		s.run(fixture.Exec("GRANT pg_read_all_settings TO replication_plain",0)); plain.replicationHeldReads(readers,true)
		s.run(fixture.Exec("REVOKE pg_read_all_settings FROM replication_plain",0)); plain.replicationHeldReads(readers,false)
		adminReaders := s.replicationPrepareReaders(true)
		s.run(fixture.Exec("SET ROLE replication_plain",0)); s.replicationHeldReads(adminReaders,false)
		s.run(fixture.Exec("RESET ROLE",0)); s.replicationHeldReads(adminReaders,true)
		inherited := s.login("replication_inherited"); inheritedReaders := inherited.replicationPrepareReaders(true)
		inherited.replicationHeldReads(inheritedReaders,true)
		s.run(fixture.Exec("REVOKE pg_read_all_settings FROM replication_bridge",0)); inherited.replicationHeldReads(inheritedReaders,false)
		s.run(fixture.Exec("GRANT pg_read_all_settings TO replication_bridge",0)); inherited.replicationHeldReads(inheritedReaders,true)
		setter := s.login("replication_setter")
		setter.run(fixture.Exec("SET output_plugin_libraries='before_revoke'",0))
		s.run(fixture.Exec("GRANT pg_read_all_settings TO replication_setter",0))
		const heldSQL = "SELECT set_config($1,$2,false)"
		mutation,err := setter.conn.PrepareContext(s.p.Ctx,heldSQL)
		if err != nil { s.t.Fatal(err) }
		defer mutation.Close()
		s.run(fixture.Exec("REVOKE SET ON PARAMETER output_plugin_libraries FROM replication_setter",0))
		fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:setter.conn,Statement:mutation,SQL:heldSQL},setter.recorder,physicalError(heldSQL,replicationSetDenied(),false,true,"output_plugin_libraries","after_set_revoke"))
		setter.replicationValue("output_plugin_libraries","before_revoke","before_revoke","session",true)
		setter.run(physicalError("RESET output_plugin_libraries",replicationSetDenied(),true,false))
		setter.run(fixture.Exec("RESET ALL",0))
		setter.replicationValue("output_plugin_libraries","pgoutput, test_decoding","pgoutput, test_decoding","default",true)
	})
}

// A SET grant permits SQL SET but cannot expose set_config's return value.
// Inspect on the same physical owner through a committed read grant outside
// transactions or an explicitly permitted reader role inside them.
func (s *physicalCase) replicationSetOnly(user, peer *physicalCase, role string, prepared bool) {
	step := fixture.Exec("SET output_plugin_libraries='set_only_before'",0)
	step.Prepared = prepared
	user.run(step)
	user.run(fixture.Exec("SET application_name='replication-set-only'",0))
	s.run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role,0))
	s.run(fixture.Exec("GRANT SELECT,INSERT ON replication_authority_outer TO "+role,0))
	s.run(fixture.Exec("GRANT replication_reader TO "+role+" WITH INHERIT FALSE, SET TRUE",0))
	check := func(want string, transaction bool) {
		overrides := map[string]string{"output_plugin_libraries":want}
		if transaction {
			overrides["wal_sender_timeout"] = "1500"
			// Aggregate filtering is non-failing inside the recovered outer
			// transaction; an individual denied read would abort it again.
			user.replicationInventory(false,prepared,overrides)
		} else {
			user.run(physicalError("SELECT current_setting('output_plugin_libraries',true)",physicalDenied("output_plugin_libraries"),prepared,true))
		}
		if transaction {
			// This same-owner transition supplies explicit examination
			// authority without depending on a peer's membership refresh
			// during the active transaction.
			user.run(fixture.Exec("SET ROLE replication_reader",0))
		} else {
			s.run(fixture.Exec("GRANT pg_read_all_settings TO "+role,0))
		}
		user.replicationValue("output_plugin_libraries",want,want,"session",prepared)
		if transaction {
			user.replicationValue("wal_sender_timeout","1500ms","1500","session",prepared)
		}
		user.replicationInventory(true,prepared,overrides)
		if transaction {
			user.run(fixture.Exec("RESET ROLE",0))
			user.run(fixture.Select("SELECT v FROM replication_authority_outer",[]string{"v"},[]string{"INT4"},[][]any{{int64(1)}}))
		} else {
			s.run(fixture.Exec("REVOKE pg_read_all_settings FROM "+role,0))
		}
		user.replicationInventory(false,prepared,overrides)
		user.run(fixture.Select("SHOW application_name",[]string{"application_name"},[]string{"TEXT"},[][]any{{"replication-set-only"}}))
		s.replicationData()
		peer.replicationInventory(true,false,nil)
	}
	const query = "SELECT set_config($1,$2,$3)"
	stmt,err := user.conn.PrepareContext(s.p.Ctx,query)
	if err != nil { s.t.Fatal(err) }
	defer stmt.Close()
	held := fixture.HeldStatement{Executor:user.conn,Statement:stmt,SQL:query}
	check("set_only_before",false) // Preparing alone cannot mutate or disclose.
	for _, local := range []any{false,nil} {
		for _, value := range []any{`not-a-real-library, "odd name"`,nil} {
			user.run(physicalError(query,physicalDenied("output_plugin_libraries"),prepared,true,"output_plugin_libraries",value,local))
			check("set_only_before",false)
		}
	}
	for _, local := range []any{false,true,nil} {
		for _, value := range []any{`not-a-real-library, "odd name"`,nil} {
			user.run(fixture.Exec("BEGIN",0))
			user.run(fixture.Exec("INSERT INTO replication_authority_outer VALUES(1)",1))
			user.run(fixture.Exec("SET wal_sender_timeout='1500ms'",0))
			user.run(fixture.Exec("SAVEPOINT replication_set_only",0))
			user.run(physicalError(query,physicalDenied("output_plugin_libraries"),prepared,true,"output_plugin_libraries",value,local))
			user.run(physicalError("SELECT 1",physicalDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block"),false,true))
			user.run(fixture.Exec("ROLLBACK TO SAVEPOINT replication_set_only",0))
			check("set_only_before",true)
			user.run(fixture.Exec("RELEASE SAVEPOINT replication_set_only",0))
			user.run(fixture.Exec("COMMIT",0))
			s.run(fixture.Select("SELECT v FROM replication_authority_outer",[]string{"v"},[]string{"INT4"},[][]any{{int64(1)}}))
			s.run(fixture.Exec("DELETE FROM replication_authority_outer",1))
			user.run(fixture.Exec("RESET wal_sender_timeout",0))
		}
	}
	user.run(fixture.Exec("BEGIN",0))
	user.run(fixture.Exec("SET wal_sender_timeout='1500ms'",0))
	user.run(fixture.Exec("INSERT INTO replication_authority_outer VALUES(1)",1))
	user.run(physicalError(query,physicalDenied("output_plugin_libraries"),prepared,true,"output_plugin_libraries","full_rollback_attempt",false))
	user.run(fixture.Exec("ROLLBACK",0))
	check("set_only_before",false)
	s.run(fixture.Select("SELECT v FROM replication_authority_outer",[]string{"v"},[]string{"INT4"},nil))
	fixture.RunStep(s.t,s.p.Ctx,held,user.recorder,physicalError(query,physicalDenied("output_plugin_libraries"),false,true,"output_plugin_libraries","held_denied",false))
	check("set_only_before",false)
	fixture.RunStep(s.t,s.p.Ctx,held,user.recorder,physicalError(query,physicalDenied("OUTPUT_PLUGIN_LIBRARIES"),false,true,"OUTPUT_PLUGIN_LIBRARIES","uppercase_rejected",false))
	check("set_only_before",false)
	s.run(fixture.Exec("GRANT pg_read_all_settings TO "+role,0))
	raw := `read_authorized, "odd name"`
	fixture.RunStep(s.t,s.p.Ctx,held,user.recorder,fixture.Select(query,[]string{"set_config"},[]string{"TEXT"},[][]any{{raw}},"output_plugin_libraries",raw,false))
	user.replicationValue("output_plugin_libraries",raw,raw,"session",prepared)
	s.run(fixture.Exec("REVOKE pg_read_all_settings FROM "+role,0))
	fixture.RunStep(s.t,s.p.Ctx,held,user.recorder,physicalError(query,physicalDenied("output_plugin_libraries"),false,true,"output_plugin_libraries","held_rejected_after_revoke",false))
	check(raw,false)
}
