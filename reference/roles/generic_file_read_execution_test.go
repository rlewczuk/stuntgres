package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func fileReadHeldNull(s *fixture.RoleMembershipScenario, held fixture.HeldStatement, name string, args ...any) {
	s.T.Helper()
	columns, types, values := []string{"v"}, []string{"TEXT"}, [][]any{{nil}}
	if name == "pg_read_binary_file" { types[0] = "BYTEA" }
	if name == "pg_stat_file" { columns = []string{"size","access","modification","change","creation","isdir"}; types = []string{"INT8","TIMESTAMPTZ","TIMESTAMPTZ","TIMESTAMPTZ","TIMESTAMPTZ","BOOL"}; values = [][]any{{nil,nil,nil,nil,nil,nil}} }
	if name == "pg_ls_tmpdir" { columns, types = fileListColumns(true); values = nil }
	fileListHeldStep(s, held, fixture.Select(held.SQL, columns, types, values, args...))
}

func TestReferenceGenericFileReadHeldACL(t *testing.T) {
	fileReadCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		admin, err := db.Conn(s.P.Ctx)
		if err != nil { s.T.Fatal(err) }; defer admin.Close()
		change := func(q string) { if _, err := admin.ExecContext(s.P.Ctx,q); err != nil { s.T.Fatal(q,err) } }
		s.Exec("CREATE TABLE gfr_sentinel(id int)")
		s.Run(fixture.Exec("INSERT INTO gfr_sentinel VALUES(7)",1))
		s.Exec("SET application_name='gfr_actor'")
		change("SET application_name='gfr_peer'")
		s.Exec("SET ROLE gfl_actor")
		for _, tc := range []struct{name, signature, query string; args []any}{
			{"pg_stat_file","pg_stat_file(text)","SELECT * FROM pg_stat_file($1::text)",[]any{nil}},
			{"pg_stat_file","pg_stat_file(text,boolean)","SELECT * FROM pg_stat_file($1::text,$2::boolean)",[]any{nil,false}},
			{"pg_ls_tmpdir","pg_ls_tmpdir(oid)","SELECT * FROM pg_ls_tmpdir($1::oid)",[]any{nil}},
		} {
			held := predefinedHeld(s,tc.query)
			fileListHeldDenied(s,held,tc.name,tc.args...)
			change("GRANT EXECUTE ON FUNCTION "+tc.signature+" TO gfl_actor")
			for range 7 { fileReadHeldNull(s,held,tc.name,tc.args...) }
			change("REVOKE EXECUTE ON FUNCTION "+tc.signature+" FROM gfl_actor")
			fileListHeldDenied(s,held,tc.name,tc.args...)
			change("GRANT EXECUTE ON FUNCTION "+tc.signature+" TO gfl_actor")
			fileReadHeldNull(s,held,tc.name,tc.args...)
		}
		for _, name := range []string{"pg_read_file","pg_read_binary_file"} {
			held := predefinedHeld(s,"SELECT "+name+"($1::text) AS v")
			// The first five PostgreSQL executions use custom plans: imported NULL
			// becomes a constant and erases the strict scalar call before admission.
			for range 5 { fileReadHeldNull(s,held,name,nil) }
			// This cheap statement then selects a generic plan on PostgreSQL 18.6.
			// Its parameter is not a constant, so the surviving call requires EXECUTE
			// even for NULL. Native preflight instead uses each imported typed frame;
			// it intentionally has no cost-based custom/generic transition.
			for range 2 {
				if s.Backend.PG {
					fileListHeldDenied(s,held,name,nil)
				} else {
					fileReadHeldNull(s,held,name,nil)
				}
			}
			fileListHeldDenied(s,held,name,"PG_VERSION")
			change("GRANT EXECUTE ON FUNCTION "+name+"(text) TO gfl_actor")
			fileReadHeldNull(s,held,name,nil)
			typ := "TEXT"
			want := "18\n"
			if name == "pg_read_binary_file" { typ = "BYTEA" }
			if s.Backend.PG {
				fileListHeldStep(s,held,fixture.Select(held.SQL,[]string{"v"},[]string{typ},[][]any{{want}},"PG_VERSION"))
			} else {
				step := fixture.ErrorDiagnostic(held.SQL,fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"0A000",Message:name+" physical file reading is unsupported in embedded mode"},"PG_VERSION")
				step.SelectRows = true; step.ErrorStage = "execute"; fileListHeldStep(s,held,step)
			}
			pathDenied := fixture.ErrorDiagnostic(held.SQL,fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42501",Message:"path must be in or below the data directory"},"../gfr_missing_497356")
			pathDenied.SelectRows = true; pathDenied.ErrorStage = "execute"
			fileListHeldStep(s,held,pathDenied)
			change("GRANT pg_read_server_files TO gfl_actor")
			d := fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"0A000",Message:name+" physical file reading is unsupported in embedded mode"}
			if s.Backend.PG { d.Code = "58P01"; d.Message = `could not open file "../gfr_missing_497356" for reading: No such file or directory` }
			step := fixture.ErrorDiagnostic(held.SQL,d,"../gfr_missing_497356")
			step.SelectRows = true; step.ErrorStage = "execute"; fileListHeldStep(s,held,step)
			change("REVOKE pg_read_server_files FROM gfl_actor")
			fileListHeldStep(s,held,pathDenied)
			change("REVOKE EXECUTE ON FUNCTION "+name+"(text) FROM gfl_actor")
			fileListHeldDenied(s,held,name,"PG_VERSION")
			if s.Backend.PG {
				fileListHeldDenied(s,held,name,nil)
			} else {
				fileReadHeldNull(s,held,name,nil)
			}
		}
		s.Exec("RESET ROLE")
		predefinedSelect(s,"SELECT id FROM gfr_sentinel",[]string{"id"},[]string{"INT4"},[][]any{{int64(7)}})
		predefinedSelect(s,"SELECT current_setting('application_name') AS v",[]string{"v"},[]string{"TEXT"},[][]any{{"gfr_actor"}})
		var peer string
		if err := admin.QueryRowContext(s.P.Ctx,"SELECT current_setting('application_name')").Scan(&peer); err != nil || peer != "gfr_peer" { s.T.Fatal(peer,err) }
	})
}

func TestReferenceGenericFileReadFeatureSmoke(t *testing.T) {
	fileReadCases(t,func(s *fixture.RoleMembershipScenario,_ *sql.DB) {
		s.Exec("CREATE TABLE gfr_sentinel(id int)")
		s.Run(fixture.Exec("INSERT INTO gfr_sentinel VALUES(7)",1))
		s.Exec("SET ROLE gfl_actor")
		fileReadNull(s,"pg_read_file(NULL::text)","pg_read_file",false)
		fileListDenied(s,"pg_stat_file(NULL::text)","pg_stat_file")
		fileListDenied(s,"pg_ls_tmpdir(NULL::oid)","pg_ls_tmpdir")
		s.Exec("RESET ROLE")
		s.Exec("GRANT EXECUTE ON FUNCTION pg_stat_file(text),pg_ls_tmpdir(),pg_ls_tmpdir(oid),pg_read_file(text),pg_read_file(text,boolean),pg_read_binary_file(text),pg_read_file(text,bigint,bigint) TO gfl_actor")
		s.Exec("SET ROLE gfl_actor")
		fileReadNull(s,"pg_stat_file(NULL::text)","pg_stat_file",true)
		fileReadNull(s,"pg_ls_tmpdir(NULL::oid)","pg_ls_tmpdir",true)
		held := predefinedHeld(s,"SELECT * FROM pg_stat_file($1::text)")
		fileReadHeldNull(s,held,"pg_stat_file",nil)
		s.Exec("RESET ROLE")
		s.Exec("REVOKE EXECUTE ON FUNCTION pg_stat_file(text) FROM gfl_actor")
		s.Exec("SET ROLE gfl_actor")
		fileListHeldDenied(s,held,"pg_stat_file",nil)
		s.Exec("RESET ROLE")
		s.Exec("GRANT EXECUTE ON FUNCTION pg_stat_file(text) TO gfl_actor")
		s.Exec("SET ROLE gfl_actor")
		fileReadHeldNull(s,held,"pg_stat_file",nil)
		predefinedFailure(s,"SELECT pg_read_file('/outside',0,-1)",fixture.Diagnostic{Code:"22023",Message:"requested length cannot be negative"},true,"execute")
		predefinedDenied(s,"SELECT pg_read_file('../x')","path must be in or below the data directory",true)
		fileReadVersion(s,"pg_read_file","'PG_VERSION'","18\n")
		fileReadVersion(s,"pg_read_binary_file","'PG_VERSION'","18\n")
		fileReadVersion(s,"pg_read_file","'PG_VERSION',true","18\n")
		fileListStockRows(s,"pg_ls_tmpdir")
		predefinedFailure(s,"SELECT * FROM pg_ls_tmpdir(0::oid)",fixture.Diagnostic{Code:"42704",Message:"tablespace with OID 0 does not exist"},true,"execute")
		s.Exec("RESET ROLE")
		predefinedSelect(s,"SELECT id FROM gfr_sentinel",[]string{"id"},[]string{"INT4"},[][]any{{int64(7)}})
		if !s.Backend.PG { fileReadControlRoundtrip(s) }
	})
}
