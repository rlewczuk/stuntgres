package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

type fileReadSignature struct {
	name, signature string
	oid int64
	args []string
}

var fileReadSignatures = []fileReadSignature{
	{"pg_read_file", "pg_read_file(text)", 3826, []string{"'PG_VERSION'::text"}},
	{"pg_read_file", "pg_read_file(text,boolean)", 6208, []string{"'PG_VERSION'::text", "false"}},
	{"pg_read_file", "pg_read_file(text,bigint,bigint)", 2624, []string{"'PG_VERSION'::text", "0::bigint", "1::bigint"}},
	{"pg_read_file", "pg_read_file(text,bigint,bigint,boolean)", 3293, []string{"'PG_VERSION'::text", "0::bigint", "1::bigint", "false"}},
	{"pg_read_binary_file", "pg_read_binary_file(text)", 3828, []string{"'PG_VERSION'::text"}},
	{"pg_read_binary_file", "pg_read_binary_file(text,boolean)", 6209, []string{"'PG_VERSION'::text", "false"}},
	{"pg_read_binary_file", "pg_read_binary_file(text,bigint,bigint)", 3827, []string{"'PG_VERSION'::text", "0::bigint", "1::bigint"}},
	{"pg_read_binary_file", "pg_read_binary_file(text,bigint,bigint,boolean)", 3295, []string{"'PG_VERSION'::text", "0::bigint", "1::bigint", "false"}},
	{"pg_stat_file", "pg_stat_file(text)", 2623, []string{"'PG_VERSION'::text"}},
	{"pg_stat_file", "pg_stat_file(text,boolean)", 3307, []string{"'PG_VERSION'::text", "false"}},
	{"pg_ls_tmpdir", "pg_ls_tmpdir()", 5029, nil},
	{"pg_ls_tmpdir", "pg_ls_tmpdir(oid)", 5030, []string{"1663::oid"}},
}

// Preserve all modified shared builtins, including PUBLIC and pg_monitor seeds.
func fileReadCases(t *testing.T, body func(*fixture.RoleMembershipScenario, *sql.DB)) {
	t.Helper()
	fileListCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		if s.Backend.PG {
			originals := make(map[int64]sql.NullString)
			for _, sig := range fileReadSignatures {
				var raw sql.NullString
				if err := s.C.QueryRowContext(s.P.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=$1", sig.oid).Scan(&raw); err != nil { s.T.Fatal(err) }
				originals[sig.oid] = raw
			}
			defer func() {
				if _, err := s.C.ExecContext(s.P.Ctx, "RESET ROLE; DROP OWNED BY gfl_actor,gfl_mid,gfl_set,gfl_delegate CASCADE"); err != nil { s.T.Error(err) }
				for oid, raw := range originals {
					var value any
					if raw.Valid { value = raw.String }
					if _, err := s.C.ExecContext(s.P.Ctx, "UPDATE pg_catalog.pg_proc SET proacl=$1::aclitem[] WHERE oid=$2", value, oid); err != nil { s.T.Error(err) }
				}
			}()
		}
		body(s, db)
	})
}

func fileReadNull(s *fixture.RoleMembershipScenario, call, name string, from bool) {
	s.T.Helper()
	q := "SELECT "+call
	columns, types, rows := []string{name}, []string{"TEXT"}, [][]any{{nil}}
	if name == "pg_read_binary_file" { types[0] = "BYTEA" }
	if name == "pg_stat_file" { types[0] = "RECORD" }
	if from || name == "pg_ls_tmpdir" {
		q = "SELECT * FROM "+call
		if name == "pg_stat_file" {
			columns = []string{"size", "access", "modification", "change", "creation", "isdir"}
			types = []string{"INT8", "TIMESTAMPTZ", "TIMESTAMPTZ", "TIMESTAMPTZ", "TIMESTAMPTZ", "BOOL"}
			rows = [][]any{{nil, nil, nil, nil, nil, nil}}
		} else if name == "pg_ls_tmpdir" {
			columns, types = fileListColumns(true)
			rows = nil
		}
	}
	predefinedSelect(s, q, columns, types, rows)
}

func TestReferenceGenericFileReadSurface(t *testing.T) {
	fileReadCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		for _,tc := range []struct{query string; types []reflect.Type}{
			{"SELECT pg_read_file(NULL::text) AS v LIMIT 0",[]reflect.Type{reflect.TypeOf("")}},
			{"SELECT pg_read_binary_file(NULL::text) AS v LIMIT 0",[]reflect.Type{reflect.TypeOf([]byte{})}},
			{"SELECT * FROM pg_stat_file(NULL::text) LIMIT 0",[]reflect.Type{reflect.TypeOf(int64(0)),reflect.TypeOf(time.Time{}),reflect.TypeOf(time.Time{}),reflect.TypeOf(time.Time{}),reflect.TypeOf(time.Time{}),reflect.TypeOf(false)}},
			{"SELECT * FROM pg_ls_tmpdir(NULL::oid)",[]reflect.Type{reflect.TypeOf(""),reflect.TypeOf(int64(0)),reflect.TypeOf(time.Time{})}},
		} {
			held := predefinedHeld(s,tc.query)
			rows,err := held.Statement.QueryContext(s.P.Ctx)
			if err != nil { s.T.Fatal(err) }
			columns,err := rows.ColumnTypes()
			if err != nil || len(columns) != len(tc.types) { rows.Close(); s.T.Fatal(columns,err) }
			for i,want := range tc.types { if columns[i].ScanType() != want { rows.Close(); s.T.Fatal(tc.query,i,columns[i].ScanType(),want) } }
			if rows.Next() || rows.Err() != nil { rows.Close(); s.T.Fatal("expected empty typed result",rows.Err()) }
			rows.Close()
		}
		for _, sig := range fileReadSignatures {
			result, set := int64(25), false
			if sig.name == "pg_read_binary_file" { result = 17 }
			if sig.name == "pg_stat_file" || sig.name == "pg_ls_tmpdir" { result = 2249 }
			if sig.name == "pg_ls_tmpdir" { set = true }
			predefinedSelect(s, fmt.Sprintf("SELECT oid::bigint AS id,prorettype::bigint AS result,proretset,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel FROM pg_proc WHERE oid=%d", sig.oid), []string{"id", "result", "proretset", "proisstrict", "volatility", "parallel"}, []string{"INT8", "INT8", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{sig.oid, result, set, true, "v", "s"}})
			predefinedSelect(s, "SELECT "+fileListLiteral(sig.signature)+"::regprocedure::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{sig.oid}})
			source := sig.name
			switch sig.oid {
			case 3826: source = "pg_read_file_all"
			case 6208: source = "pg_read_file_all_missing"
			case 2624: source = "pg_read_file_off_len"
			case 3293: source = "pg_read_file_off_len_missing"
			case 3828: source = "pg_read_binary_file_all"
			case 6209: source = "pg_read_binary_file_all_missing"
			case 3827: source = "pg_read_binary_file_off_len"
			case 3295: source = "pg_read_binary_file_off_len_missing"
			case 2623: source = "pg_stat_file_1arg"
			case 5029: source = "pg_ls_tmpdir_noargs"
			case 5030: source = "pg_ls_tmpdir_1arg"
			}
			predefinedSelect(s, fmt.Sprintf("SELECT prosrc FROM pg_proc WHERE oid=%d",sig.oid), []string{"prosrc"}, []string{"TEXT"}, [][]any{{source}})
			for i := range sig.args {
				args := append([]string(nil), sig.args...)
				typ := "text"
				if i > 0 { typ = "bigint"; if len(args) == 2 || i == 3 { typ = "boolean" } }
				if sig.name == "pg_ls_tmpdir" { typ = "oid" }
				args[i] = "NULL::"+typ
				call := sig.name+"("+strings.Join(args, ",")+")"
				fileReadNull(s, call, sig.name, false)
				fileReadNull(s, call, sig.name, true)
			}
		}
		predefinedSelect(s,"SELECT proargnames::text,proargmodes::text,proallargtypes::text FROM pg_proc WHERE oid=3307",[]string{"proargnames","proargmodes","proallargtypes"},[]string{"TEXT","TEXT","TEXT"},[][]any{{"{filename,missing_ok,size,access,modification,change,creation,isdir}","{i,i,o,o,o,o,o,o}","{25,16,20,1184,1184,1184,1184,16}"}})
		predefinedSelect(s,"SELECT proargnames::text FROM pg_proc WHERE oid=5030",[]string{"proargnames"},[]string{"TEXT"},[][]any{{"{tablespace,name,size,modification}"}})
		for _,sig := range fileReadSignatures[:8] {
			predefinedSelect(s,fmt.Sprintf("SELECT proargnames IS NULL AS unnamed FROM pg_proc WHERE oid=%d",sig.oid),[]string{"unnamed"},[]string{"BOOL"},[][]any{{true}})
		}
		fileReadNull(s, "pg_stat_file(missing_ok => NULL::boolean,filename => 'PG_VERSION')", "pg_stat_file", true)
		fileReadNull(s, "pg_stat_file(filename => NULL::text)", "pg_stat_file", false)
		fileReadNull(s, "pg_ls_tmpdir(tablespace => NULL::oid)", "pg_ls_tmpdir", true)
		predefinedSelect(s, "SELECT * FROM pg_ls_tmpdir(NULL::oid) WITH ORDINALITY AS f(n,s,m,o)", []string{"n", "s", "m", "o"}, []string{"TEXT", "INT8", "TIMESTAMPTZ", "INT8"}, nil)
		predefinedSelect(s, "SELECT (pg_stat_file(NULL::text)).*", []string{"size", "access", "modification", "change", "creation", "isdir"}, []string{"INT8", "TIMESTAMPTZ", "TIMESTAMPTZ", "TIMESTAMPTZ", "TIMESTAMPTZ", "BOOL"}, [][]any{{nil,nil,nil,nil,nil,nil}})
	})
}
