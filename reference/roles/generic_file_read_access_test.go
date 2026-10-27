package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGenericFileReadAuthorization(t *testing.T) {
	fileReadCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		for _, sig := range fileReadSignatures {
			s.Exec("SET ROLE gfl_actor")
			fileListDenied(s, sig.name+"("+strings.Join(sig.args, ",")+")", sig.name)
			s.Exec("RESET ROLE")
			s.Exec("GRANT EXECUTE ON FUNCTION "+sig.signature+" TO gfl_actor")
			predefinedSelect(s, "SELECT has_function_privilege('gfl_actor',"+fileListLiteral(sig.signature)+",'EXECUTE') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}})
			s.Exec("SET ROLE gfl_actor")
			if len(sig.args) == 0 {
				fileListStockRows(s,"pg_ls_tmpdir")
			} else {
				args := append([]string(nil),sig.args...)
				args[0] = "NULL::text"
				if sig.name == "pg_ls_tmpdir" { args[0] = "NULL::oid" }
				fileReadNull(s,sig.name+"("+strings.Join(args,",")+")",sig.name,true)
			}
			s.Exec("RESET ROLE")
			s.Exec("REVOKE EXECUTE ON FUNCTION "+sig.signature+" FROM gfl_actor")
			predefinedSelect(s, "SELECT has_function_privilege('gfl_actor',"+fileListLiteral(sig.signature)+",'EXECUTE') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}})
		}
		s.Exec("GRANT pg_monitor TO gfl_mid")
		s.Exec("GRANT gfl_mid TO gfl_actor")
		s.Exec("GRANT pg_monitor TO gfl_set WITH INHERIT FALSE, SET TRUE")
		s.Exec("SET ROLE gfl_actor")
		fileReadNull(s, "pg_ls_tmpdir(NULL::oid)", "pg_ls_tmpdir", true)
		fileListDenied(s, "pg_stat_file(NULL::text)", "pg_stat_file")
		s.Exec("RESET ROLE")
		s.Exec("SET ROLE gfl_set")
		fileListDenied(s, "pg_ls_tmpdir(NULL::oid)", "pg_ls_tmpdir")
		s.Exec("RESET ROLE")
		s.Exec("REVOKE gfl_mid FROM gfl_actor")
		s.Exec("GRANT pg_read_server_files TO gfl_actor")
		s.Exec("SET ROLE gfl_actor")
		fileListDenied(s, "pg_read_file('PG_VERSION')", "pg_read_file")
		fileListDenied(s, "pg_read_binary_file('PG_VERSION')", "pg_read_binary_file")
		s.Exec("RESET ROLE")
		s.Exec("GRANT EXECUTE ON FUNCTION pg_stat_file(text) TO PUBLIC")
		s.Exec("SET ROLE gfl_actor")
		fileReadNull(s, "pg_stat_file(NULL::text)", "pg_stat_file", true)
		fileListDenied(s, "pg_stat_file(NULL::text,false)", "pg_stat_file")
		s.Exec("RESET ROLE")
	})
}

func TestReferenceGenericFileReadStrictness(t *testing.T) {
	fileReadCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		s.Exec("CREATE TABLE gfr_nulls(filename text,off bigint,len bigint,missing boolean)")
		s.Run(fixture.Exec("INSERT INTO gfr_nulls VALUES(NULL,NULL,NULL,NULL)",1))
		s.Exec("SET ROLE gfl_actor")
		for _,sig := range fileReadSignatures[:8] {
			for i := range sig.args {
				args := append([]string(nil),sig.args...)
				typ := "text"
				if i > 0 { typ = "bigint"; if len(args) == 2 || i == 3 { typ = "boolean" } }
				args[i] = "NULL::"+typ
				fileReadNull(s,sig.name+"("+strings.Join(args,",")+")",sig.name,false)
			}
		}
		for _, name := range []string{"pg_read_file", "pg_read_binary_file"} {
			for _, args := range []string{"NULL::text", "'PG_VERSION',NULL::boolean", "'PG_VERSION',NULL::bigint,-1::bigint", "'PG_VERSION',0::bigint,NULL::bigint", "'PG_VERSION',0::bigint,-1::bigint,NULL::boolean"} {
				fileReadNull(s, name+"("+args+")", name, false)
			}
		}
		fileListDenied(s, "pg_stat_file(NULL::text)", "pg_stat_file")
		fileListDenied(s, "pg_stat_file('PG_VERSION',NULL::boolean)", "pg_stat_file")
		fileListDenied(s, "pg_stat_file(NULL::text,false)", "pg_stat_file")
		fileListDenied(s, "pg_ls_tmpdir(NULL::oid)", "pg_ls_tmpdir")
		s.Exec("RESET ROLE")
		s.Exec("GRANT SELECT ON gfr_nulls TO gfl_actor")
		s.Exec("SET ROLE gfl_actor")
		for _, name := range []string{"pg_read_file", "pg_read_binary_file", "pg_stat_file"} {
			predefinedDenied(s, "SELECT "+name+"(filename) FROM gfr_nulls", "permission denied for function "+name, true)
		}
		s.Exec("RESET ROLE")
		for _, name := range []string{"pg_read_file", "pg_read_binary_file"} {
			predefinedFailure(s, "SELECT "+name+"(NULL::text,'bad'::bigint,1)", fixture.Diagnostic{Code:"22P02", Message:`invalid input syntax for type bigint: "bad"`, Position:strconv.Itoa(len("SELECT "+name+"(NULL::text,")+1)}, true, "prepare")
			predefinedFailure(s,"SELECT "+name+"(NULL::text,0,1/0)",fixture.Diagnostic{Code:"22012",Message:"division by zero"},true,"execute")
			s.Exec("SET application_name='gfr_before'")
			fileReadNull(s,name+"(NULL::text,0,length(set_config('application_name','gfr_discarded',false))::bigint)",name,false)
			predefinedSelect(s,"SELECT current_setting('application_name') AS v",[]string{"v"},[]string{"TEXT"},[][]any{{"gfr_before"}})
		}
	})
}
