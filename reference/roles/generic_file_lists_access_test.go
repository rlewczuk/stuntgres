package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceGenericFileListsAuthorization(t *testing.T) {
	fileListCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		s.Exec("SET ROLE gfl_actor")
		for _, sig := range fileListSignatures {
			fileListDenied(s, sig.name+"("+sig.args+")", sig.name)
		}
		s.Exec("RESET ROLE")
		s.Exec("GRANT pg_monitor TO gfl_mid")
		s.Exec("GRANT gfl_mid TO gfl_actor")
		s.Exec("GRANT pg_monitor TO gfl_set WITH INHERIT FALSE, SET TRUE")
		for _, role := range []string{"gfl_actor", "gfl_set"} {
			s.Exec("SET ROLE " + role)
			fileListDenied(s, "pg_ls_dir(NULL::text)", "pg_ls_dir")
			fileListDenied(s, "pg_ls_dir(NULL::text,false,false)", "pg_ls_dir")
			for _, sig := range fileListSignatures[2:] {
				if role == "gfl_set" {
					fileListDenied(s, sig.name+"("+sig.args+")", sig.name)
				} else if sig.args != "" {
					fileListEmpty(s, sig.name+"("+sig.args+")", true)
				} else {
					fileListStockRows(s, sig.name)
				}
			}
			if role == "gfl_set" {
				s.Exec("SET ROLE pg_monitor")
				fileListEmpty(s, "pg_ls_replslotdir(NULL::text)", true)
			}
			s.Exec("RESET ROLE")
		}
		s.Exec("REVOKE gfl_mid FROM gfl_actor")
		s.Exec("GRANT pg_read_server_files TO gfl_actor")
		s.Exec("SET ROLE gfl_actor")
		fileListDenied(s, "pg_ls_dir(NULL::text)", "pg_ls_dir")
		s.Exec("RESET ROLE")
		for _, sig := range fileListSignatures {
			s.Exec("GRANT EXECUTE ON FUNCTION " + sig.signature + " TO gfl_actor")
			s.Exec("SET ROLE gfl_actor")
			if sig.args != "" {
				fileListEmpty(s, sig.name+"("+sig.args+")", sig.record)
			} else {
				fileListStockRows(s, sig.name)
			}
			s.Exec("RESET ROLE")
			s.Exec("REVOKE EXECUTE ON FUNCTION " + sig.signature + " FROM gfl_actor")
			s.Exec("SET ROLE gfl_actor")
			fileListDenied(s, sig.name+"("+sig.args+")", sig.name)
			s.Exec("RESET ROLE")
		}
		defer func() {
			if _, err := s.C.ExecContext(s.P.Ctx, "RESET ROLE"); err != nil {
				s.T.Error(err)
			}
			if _, err := s.C.ExecContext(s.P.Ctx, "REVOKE EXECUTE ON FUNCTION pg_ls_dir(text) FROM PUBLIC"); err != nil {
				s.T.Error(err)
			}
		}()
		s.Exec("GRANT EXECUTE ON FUNCTION pg_ls_dir(text) TO PUBLIC")
		s.Exec("SET ROLE gfl_actor")
		fileListEmpty(s, "pg_ls_dir(NULL::text)", false)
		fileListDenied(s, "pg_ls_dir(NULL::text,false,false)", "pg_ls_dir")
		s.Exec("RESET ROLE")
		s.Exec("GRANT EXECUTE ON FUNCTION pg_ls_dir(text,boolean,boolean) TO gfl_actor")
		s.Exec("SET ROLE gfl_actor")
		fileListMissing(s, "SELECT * FROM pg_ls_dir('/gfl_nonexistent_497356',true,false)", "/gfl_nonexistent_497356", true)
		s.Exec("RESET ROLE")
	})
}
func TestReferenceGenericFileListsPaths(t *testing.T) {
	fileListCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		var data string
		if err := s.C.QueryRowContext(s.P.Ctx, "SHOW data_directory").Scan(&data); err != nil {
			s.T.Fatal(err)
		}
		s.Exec("GRANT EXECUTE ON FUNCTION pg_ls_dir(text),pg_ls_dir(text,boolean,boolean) TO gfl_actor")
		s.Exec("SET ROLE gfl_actor")
		for _, tc := range []struct{ input, canonical string }{{"gfl_missing_497356", "gfl_missing_497356"}, {"gfl_missing_497356//./a/../", "gfl_missing_497356"}, {`gfl_missing_497356\..\x`, `gfl_missing_497356\..\x`}, {data + "/gfl_missing_497356", data + "/gfl_missing_497356"}} {
			fileListMissing(s, "SELECT * FROM pg_ls_dir("+fileListLiteral(tc.input)+")", tc.canonical, false)
			for _, missing := range []bool{false, true} {
				for _, dot := range []bool{false, true} {
					q := fmt.Sprintf("SELECT * FROM pg_ls_dir(%s,%t,%t)", fileListLiteral(tc.input), missing, dot)
					fileListMissing(s, q, tc.canonical, missing)
				}
			}
		}
		for _, value := range []string{"../", "a/../../x", "../../x"} {
			predefinedDenied(s, "SELECT * FROM pg_ls_dir("+fileListLiteral(value)+",true,true)", "path must be in or below the data directory", true)
		}
		for _, value := range []string{"/etc", "/tmp/../etc", data + "-sibling/x", data + "base", data + "/../gfl_outside"} {
			predefinedDenied(s, "SELECT * FROM pg_ls_dir("+fileListLiteral(value)+",true,false)", "absolute path not allowed", true)
		}
		// Empty canonical input is not silently replaced with '.', even though native
		// physical requests deliberately terminate at the same mechanism boundary.
		fileListMissing(s, "SELECT * FROM pg_ls_dir('',true,false)", "", true)
		s.Exec("RESET ROLE")
		s.Exec("GRANT pg_read_server_files TO gfl_actor")
		s.Exec("SET ROLE gfl_actor")
		fileListMissing(s, "SELECT * FROM pg_ls_dir('/gfl_nonexistent_497356',true,false)", "/gfl_nonexistent_497356", true)
		s.Exec("RESET ROLE")
	})
}
func TestReferenceGenericFileListsDiagnostics(t *testing.T) {
	fileListCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		cases := []struct{ q, message string }{
			{"SELECT * FROM pg_ls_dir('x',true)", "function pg_ls_dir(unknown, boolean) does not exist"},
			{"SELECT * FROM pg_ls_dir(1)", "function pg_ls_dir(integer) does not exist"},
			{"SELECT * FROM pg_ls_dir(NULL,1,false)", "function pg_ls_dir(unknown, integer, boolean) does not exist"},
			{"SELECT * FROM pg_ls_dir(dirname => 'x')", "function pg_ls_dir(dirname => unknown) does not exist"},
			{"SELECT * FROM pg_ls_dir('x',missing_ok => true,include_dot_dirs => false)", "function pg_ls_dir(unknown, missing_ok => boolean, include_dot_dirs => boolean) does not exist"},
			{"SELECT * FROM pg_ls_replslotdir(other => NULL::text)", "function pg_ls_replslotdir(other => text) does not exist"},
			{`SELECT * FROM "PG_LS_DIR"('x')`, `function PG_LS_DIR(unknown) does not exist`},
			{"SELECT * FROM public.pg_ls_dir('x')", "function public.pg_ls_dir(unknown) does not exist"},
			{"SELECT 'é' AS prefix, pg_ls_dir('x',true)", "function pg_ls_dir(unknown, boolean) does not exist"},
		}
		for _, tc := range cases {
			position := "15"
			if tc.q == "SELECT 'é' AS prefix, pg_ls_dir('x',true)" {
				position = strconv.Itoa(utf8.RuneCountInString("SELECT 'é' AS prefix, ") + 1)
			}
			predefinedFailure(s, tc.q, fixture.Diagnostic{Code: "42883", Message: tc.message, Hint: fileListSignatureHint, Position: position}, true, "prepare")
		}
		s.Exec("SET ROLE gfl_actor")
		fileListDenied(s, "pg_ls_dir(NULL::text)", "pg_ls_dir")
		s.Exec("RESET ROLE")
		s.Exec("GRANT EXECUTE ON FUNCTION pg_ls_dir(text) TO gfl_actor")
		s.Exec("SET ROLE gfl_actor")
		predefinedDenied(s, "SELECT * FROM pg_ls_dir('../x')", "path must be in or below the data directory", true)
		predefinedDenied(s, "SELECT * FROM pg_ls_dir('/etc')", "absolute path not allowed", true)
		s.Exec("RESET ROLE")
		fileListMissing(s, "SELECT * FROM pg_ls_dir('gfl_missing_497356')", "gfl_missing_497356", false)
	})
}
