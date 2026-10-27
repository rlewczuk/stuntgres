package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGenericFileListsSignatures(t *testing.T) {
	fileListCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		for _, sig := range fileListSignatures {
			predefinedSelect(s, "SELECT "+fileListLiteral(sig.signature)+"::regprocedure::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{sig.oid}})
			q := fmt.Sprintf("SELECT proisstrict,provolatile,proparallel,proretset,pronargdefaults FROM pg_proc WHERE oid=%d", sig.oid)
			predefinedSelect(s, q, []string{"proisstrict", "provolatile", "proparallel", "proretset", "pronargdefaults"}, []string{"BOOL", "CHAR", "CHAR", "BOOL", "INT2"}, [][]any{{true, "v", "s", true, int64(0)}})
			columns, types := fileListColumns(sig.record)
			predefinedSelect(s, "SELECT * FROM "+sig.name+"("+sig.args+") LIMIT 0", columns, types, nil)
			wrong := "1"
			if sig.args != "" {
				wrong = ""
			}
			typesText := "integer"
			if wrong == "" {
				typesText = ""
			}
			q = "SELECT * FROM " + sig.name + "(" + wrong + ")"
			predefinedFailure(s, q, fixture.Diagnostic{Code: "42883", Message: "function " + sig.name + "(" + typesText + ") does not exist", Hint: fileListSignatureHint, Position: "15"}, true, "prepare")
		}
		fileListEmpty(s, "pg_catalog.PG_LS_DIR(NULL::text)", false)
		fileListEmpty(s, "pg_ls_replslotdir(slot_name => NULL::text)", true)
		// Exact released input/output declaration facts, including unnamed dirname.
		predefinedSelect(s, "SELECT proargnames::text,proargmodes::text,proallargtypes::text FROM pg_proc WHERE oid=6272", []string{"proargnames", "proargmodes", "proallargtypes"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"{slot_name,name,size,modification}", "{i,o,o,o}", "{25,25,20,1184}"}})
		predefinedSelect(s, "SELECT proargnames IS NULL AS unnamed,proargtypes::text FROM pg_proc WHERE oid=3297", []string{"unnamed", "proargtypes"}, []string{"BOOL", "TEXT"}, [][]any{{true, "25 16 16"}})
	})
}

const fileListSignatureHint = "No function matches the given name and argument types. You might need to add explicit type casts."

func TestReferenceGenericFileListsMetadata(t *testing.T) {
	fileListCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		for _, sig := range fileListSignatures {
			columns, types := fileListColumns(sig.record)
			q := "SELECT * FROM " + sig.name + "(" + sig.args + ") LIMIT 0"
			predefinedSelect(s, q, columns, types, nil)
			held := predefinedHeld(s, q)
			for range 2 {
				fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Select(q, columns, types, nil))
			}
		}
		for _, call := range []string{"pg_ls_dir(NULL::text)", "pg_ls_dir(NULL::text,false,false)", "pg_ls_replslotdir(NULL::text)"} {
			record := strings.HasPrefix(call, "pg_ls_replslotdir(")
			columns, types := fileListColumns(record)
			predefinedSelect(s, "SELECT * FROM "+call+" WITH ORDINALITY", append(columns, "ordinality"), append(types, "INT8"), nil)
		}
		predefinedSelect(s, "SELECT * FROM pg_ls_replslotdir(NULL::text) AS x(a)", []string{"a", "size", "modification"}, []string{"TEXT", "INT8", "TIMESTAMPTZ"}, nil)
		predefinedSelect(s, "SELECT pg_ls_replslotdir(NULL::text)", []string{"pg_ls_replslotdir"}, []string{"RECORD"}, nil)
		for _, sig := range fileListSignatures {
			typ := "TEXT"
			if sig.record {
				typ = "RECORD"
			}
			predefinedSelect(s, "SELECT "+sig.name+"("+sig.args+") LIMIT 0", []string{sig.name}, []string{typ}, nil)
		}
	})
}
func TestReferenceGenericFileListsNulls(t *testing.T) {
	fileListCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		calls := []string{"pg_ls_dir(NULL::text)", "pg_ls_dir(NULL::text,false,false)", "pg_ls_dir('.',NULL::boolean,false)", "pg_ls_dir('.',false,NULL::boolean)", "pg_ls_dir(NULL::text,NULL::boolean,false)", "pg_ls_dir(NULL::text,NULL::boolean,NULL::boolean)", "pg_ls_replslotdir(NULL::text)", "pg_ls_replslotdir(slot_name => NULL::text)"}
		for _, call := range calls {
			record := strings.HasPrefix(call, "pg_ls_replslotdir(")
			fileListEmpty(s, call, record)
			typ, name := "TEXT", "pg_ls_dir"
			if record {
				typ, name = "RECORD", "pg_ls_replslotdir"
			}
			predefinedSelect(s, "SELECT "+call, []string{name}, []string{typ}, nil)
		}
		for _, q := range []string{"SELECT * FROM pg_ls_dir($1::text)", "SELECT * FROM pg_ls_dir('.',$1::boolean,false)", "SELECT * FROM pg_ls_dir('.',false,$1::boolean)", "SELECT * FROM pg_ls_replslotdir($1::text)"} {
			record := strings.HasPrefix(q, "SELECT * FROM pg_ls_replslotdir(")
			columns, types := fileListColumns(record)
			s.Run(fixture.Select(q, columns, types, nil, nil))
		}
		s.Exec("SET ROLE gfl_actor")
		for _, call := range calls {
			name := "pg_ls_dir"
			if strings.HasPrefix(call, "pg_ls_replslotdir(") {
				name = "pg_ls_replslotdir"
			}
			fileListDenied(s, call, name)
		}
		s.Exec("RESET ROLE")
		for _, q := range []string{"SELECT * FROM pg_ls_dir(NULL,'not_bool',false)", "SELECT * FROM pg_ls_dir(NULL,false,'not_bool')"} {
			// Literal conversion is analysis-time, even when strict NULL would empty.
			pos := fmt.Sprint(strings.Index(q, "'not_bool'") + 1)
			predefinedFailure(s, q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "not_bool"`, Position: pos}, true, "prepare")
		}
	})
}
