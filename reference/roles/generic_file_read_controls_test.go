package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fileReadControlRoundtrip(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	s.Exec("SET ROLE gfl_actor")
	held := predefinedHeld(s,"SELECT * FROM pg_stat_file($1::text)")
	fileReadHeldNull(s,held,"pg_stat_file",nil)
	s.Exec("RESET ROLE")
	var snapshot bytes.Buffer
	if err := stuntgres.Dump(s.P.Ctx,s.P.Native,&snapshot); err != nil { s.T.Fatal(err) }
	if err := stuntgres.Reset(s.P.Ctx,s.P.Native); err != nil { s.T.Fatal(err) }
	if err := stuntgres.Restore(s.P.Ctx,s.P.Native,bytes.NewReader(snapshot.Bytes())); err != nil { s.T.Fatal(err) }
	s.Exec("SET ROLE gfl_actor")
	fileReadHeldNull(s,held,"pg_stat_file",nil)
	fileReadNull(s,"pg_stat_file(NULL::text)","pg_stat_file",true)
	fileListDenied(s,"pg_stat_file(NULL::text,false)","pg_stat_file")
	fileReadNull(s,"pg_ls_tmpdir(NULL::oid)","pg_ls_tmpdir",true)
	s.Exec("RESET ROLE")
	if err := stuntgres.Restore(s.P.Ctx,s.P.Native,strings.NewReader(snapshot.String()+"CREATE TABLE gfr_broken(")); err == nil { s.T.Fatal("malformed restore accepted") }
	s.Exec("SET ROLE gfl_actor")
	fileReadHeldNull(s,held,"pg_stat_file",nil)
	fileListDenied(s,"pg_stat_file(NULL::text,false)","pg_stat_file")
	s.Exec("RESET ROLE")
	predefinedSelect(s,"SELECT id FROM gfr_sentinel",[]string{"id"},[]string{"INT4"},[][]any{{int64(7)}})
	driver := &stuntgres.Driver{}
	connector,err := driver.OpenConnector("gfr_fresh")
	if err != nil { s.T.Fatal(err) }
	fresh := sql.OpenDB(connector); defer fresh.Close()
	if _,err := fresh.ExecContext(s.P.Ctx,"CREATE TABLE gfr_destination(id int); INSERT INTO gfr_destination VALUES(91)"); err != nil { s.T.Fatal(err) }
	if err := stuntgres.Restore(s.P.Ctx,fresh,bytes.NewReader(snapshot.Bytes())); err == nil { s.T.Fatal("missing role prerequisites accepted") }
	var sentinel int
	if err := fresh.QueryRowContext(s.P.Ctx,"SELECT id FROM gfr_destination").Scan(&sentinel); err != nil || sentinel != 91 { s.T.Fatal(sentinel,err) }
	for _,role := range []string{"gfl_actor","gfl_mid","gfl_set","gfl_delegate"} { if _,err := fresh.ExecContext(s.P.Ctx,"CREATE ROLE "+role+" LOGIN"); err != nil { s.T.Fatal(err) } }
	if err := stuntgres.Restore(s.P.Ctx,fresh,bytes.NewReader(snapshot.Bytes())); err != nil { s.T.Fatal(err) }
	var allowed bool
	for _,tc := range []struct{signature string; want bool}{{"pg_stat_file(text)",true},{"pg_stat_file(text,boolean)",false},{"pg_ls_tmpdir(oid)",true}} {
		if err := fresh.QueryRowContext(s.P.Ctx,"SELECT has_function_privilege('gfl_actor',$1,'EXECUTE')",tc.signature).Scan(&allowed); err != nil || allowed != tc.want { s.T.Fatal(tc,allowed,err) }
	}
}

func TestReferenceGenericFileReadControls(t *testing.T) {
	fileReadCases(t,func(s *fixture.RoleMembershipScenario,_ *sql.DB) {
		s.Exec("CREATE TABLE gfr_sentinel(id int)")
		s.Run(fixture.Exec("INSERT INTO gfr_sentinel VALUES(7)",1))
		s.Exec("GRANT EXECUTE ON FUNCTION pg_stat_file(text),pg_ls_tmpdir(oid) TO gfl_actor")
		predefinedSelect(s,"SELECT has_function_privilege('gfl_actor','pg_stat_file(text)','EXECUTE') AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}})
		predefinedSelect(s,"SELECT has_function_privilege('gfl_actor','pg_stat_file(text,boolean)','EXECUTE') AS v",[]string{"v"},[]string{"BOOL"},[][]any{{false}})
		if !s.Backend.PG {
			fileReadControlRoundtrip(s)
			// Force each exact emitted locator through absent, explicit-empty and
			// explicit ACL repair, retaining all unrelated dump statements. Absent
			// ACLs must retain the emitter's qualified aclitem[] cast; bare NULL is
			// not a canonical Restore repair.
			for _,sig := range fileReadSignatures {
				s.Exec("REVOKE ALL ON FUNCTION "+sig.signature+" FROM postgres,pg_monitor,PUBLIC")
			}
			var snapshot bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx,s.P.Native,&snapshot); err != nil { s.T.Fatal(err) }
			for _,raw := range []string{"NULL::pg_catalog.aclitem[]", `pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[]`} {
				lines := strings.Split(snapshot.String(),"\n")
				count := 0
				for i,line := range lines {
					if !strings.HasPrefix(line,"UPDATE pg_catalog.pg_proc SET proacl=") { continue }
					if !strings.Contains(line,"pg_read_file") && !strings.Contains(line,"pg_read_binary_file") && !strings.Contains(line,"pg_stat_file") && !strings.Contains(line,"pg_ls_tmpdir") { continue }
					_,locator,ok := strings.Cut(line," WHERE "); if !ok { s.T.Fatal(line) }
					lines[i] = "UPDATE pg_catalog.pg_proc SET proacl="+raw+" WHERE "+locator
					count++
				}
				if count != 12 { s.T.Fatal("missing overload repairs",count) }
				if err := stuntgres.Restore(s.P.Ctx,s.P.Native,strings.NewReader(strings.Join(lines,"\n"))); err != nil { s.T.Fatal(err) }
				var redump bytes.Buffer
				if err := stuntgres.Dump(s.P.Ctx,s.P.Native,&redump); err != nil { s.T.Fatal(err) }
				if err := stuntgres.Restore(s.P.Ctx,s.P.Native,bytes.NewReader(redump.Bytes())); err != nil { s.T.Fatal(err) }
				for _,sig := range fileReadSignatures {
					var acl sql.NullString
					if err := s.C.QueryRowContext(s.P.Ctx,"SELECT proacl::text FROM pg_proc WHERE oid=$1",sig.oid).Scan(&acl); err != nil { s.T.Fatal(err) }
					if raw == "NULL::pg_catalog.aclitem[]" && acl.Valid || raw != "NULL::pg_catalog.aclitem[]" && (!acl.Valid || acl.String != "{}") { s.T.Fatal(sig.signature,raw,acl) }
				}
			}
		}
	})
}
