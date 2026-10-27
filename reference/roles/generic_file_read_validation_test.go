package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func fileReadUnsupported(s *fixture.RoleMembershipScenario, q, name string) {
	s.T.Helper()
	mechanism := "physical file reading"
	if name == "pg_stat_file" { mechanism = "physical file metadata" }
	if name == "pg_ls_tmpdir" { mechanism = "physical directory listing" }
	predefinedFailure(s, q, fixture.Diagnostic{Code:"0A000", Message:name+" "+mechanism+" is unsupported in embedded mode"}, true, "execute")
}

func fileReadVersion(s *fixture.RoleMembershipScenario, name, args, want string) {
	s.T.Helper()
	q := "SELECT "+name+"("+args+") AS v"
	if !s.Backend.PG { fileReadUnsupported(s, q, name); return }
	typ := "TEXT"
	if name == "pg_read_binary_file" { typ = "BYTEA" }
	predefinedSelect(s, q, []string{"v"}, []string{typ}, [][]any{{want}})
}

func TestReferenceGenericFileReadValidation(t *testing.T) {
	fileReadCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		var data string
		if err := s.C.QueryRowContext(s.P.Ctx,"SHOW data_directory").Scan(&data); err != nil { s.T.Fatal(err) }
		s.Exec("GRANT EXECUTE ON FUNCTION pg_read_file(text,bigint,bigint),pg_read_binary_file(text,bigint,bigint) TO gfl_actor")
		s.Exec("SET ROLE gfl_actor")
		for _, name := range []string{"pg_read_file", "pg_read_binary_file"} {
			for _, path := range []string{"PG_VERSION", "/outside"} {
				predefinedFailure(s, "SELECT "+name+"("+fileListLiteral(path)+",0,-1)", fixture.Diagnostic{Code:"22023", Message:"requested length cannot be negative"}, true, "execute")
			}
			for _, length := range []string{"1073741820", "9223372036854775807"} {
				predefinedFailure(s, "SELECT "+name+"('PG_VERSION',0,"+length+")", fixture.Diagnostic{Code:"22023", Message:"requested length too large"}, true, "execute")
				predefinedDenied(s, "SELECT "+name+"('/outside',0,"+length+")", "absolute path not allowed", true)
			}
			for _, path := range []string{"../", "a/../../x", "../../x"} {
				predefinedDenied(s, "SELECT "+name+"("+fileListLiteral(path)+",0,1)", "path must be in or below the data directory", true)
			}
			for _,path := range []string{data+"-sibling/x",data+"/../gfr_outside","/tmp/../etc"} {
				predefinedDenied(s,"SELECT "+name+"("+fileListLiteral(path)+",0,1)","absolute path not allowed",true)
			}
			for _,tc := range []struct{path,canonical string}{
				{"",""},{"gfr_missing_497356//./a/../","gfr_missing_497356"},
				{`gfr_missing_497356\..\x`,`gfr_missing_497356\..\x`},
				{data+"/gfr_missing_497356",data+"/gfr_missing_497356"},
			} {
				q := "SELECT "+name+"("+fileListLiteral(tc.path)+",0,1)"
				if !s.Backend.PG { fileReadUnsupported(s,q,name) } else {
					predefinedFailure(s,q,fixture.Diagnostic{Code:"58P01",Message:fmt.Sprintf("could not open file \"%s\" for reading: No such file or directory",tc.canonical)},true,"execute")
				}
			}
			fileReadNull(s, name+"(NULL::text,0,-1)", name, false)
			fileReadVersion(s, name, "'PG_VERSION',0,1073741819", "18\n")
			fileReadVersion(s, name, "'PG_VERSION',0,0", "")
		}
		s.Exec("RESET ROLE")
		for _, oid := range []string{"0", "4294967295"} {
			predefinedFailure(s, "SELECT * FROM pg_ls_tmpdir("+oid+"::oid)", fixture.Diagnostic{Code:"42704", Message:"tablespace with OID "+oid+" does not exist"}, true, "execute")
		}
	})
}

func TestReferenceGenericFileReadDiagnostics(t *testing.T) {
	fileReadCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		for _, tc := range []struct{call, signature string}{
			{"pg_read_file()", "pg_read_file()"}, {"pg_read_file(1)", "pg_read_file(integer)"},
			{"pg_read_binary_file(true)", "pg_read_binary_file(boolean)"},
			{"pg_read_file('x',1)", "pg_read_file(unknown, integer)"},
			{"pg_read_file('x',true,true)", "pg_read_file(unknown, boolean, boolean)"},
			{"pg_read_file('x',0,1,false,1)", "pg_read_file(unknown, integer, integer, boolean, integer)"},
			{"pg_stat_file()", "pg_stat_file()"}, {"pg_stat_file('x',0,1)", "pg_stat_file(unknown, integer, integer)"},
			{"pg_ls_tmpdir(true)", "pg_ls_tmpdir(boolean)"}, {"pg_ls_tmpdir(0,1)", "pg_ls_tmpdir(integer, integer)"},
			{"pg_read_file(filename => 'x')", "pg_read_file(filename => unknown)"},
			{"pg_stat_file(path => 'x')", "pg_stat_file(path => unknown)"},
			{"pg_ls_tmpdir(space => NULL::oid)", "pg_ls_tmpdir(space => oid)"},
			{"public.pg_read_file('x')", "public.pg_read_file(unknown)"},
			{`"PG_READ_FILE"('x')`, "PG_READ_FILE(unknown)"},
		} {
			predefinedFailure(s, "SELECT "+tc.call, fixture.Diagnostic{Code:"42883", Message:"function "+tc.signature+" does not exist", Hint:fileListSignatureHint, Position:"8"}, true, "prepare")
		}
		predefinedFailure(s, "SELECT 'é', pg_read_file(1)", fixture.Diagnostic{Code:"42883", Message:"function pg_read_file(integer) does not exist", Hint:fileListSignatureHint, Position:"13"}, true, "prepare")
		for _,name := range []string{"pg_read_file","pg_read_binary_file"} {
			// No overload accepts an integer flag. Reject that typed axis before
			// attempting to coerce the earlier UNKNOWN offset to bigint.
			for _, offset := range []string{"0", "'bad'"} {
				q := "SELECT "+name+"('x',"+offset+",1,0)"
				offsetType := "integer"
				if offset == "'bad'" { offsetType = "unknown" }
				predefinedFailure(s,q,fixture.Diagnostic{Code:"42883",Message:"function "+name+"(unknown, "+offsetType+", integer, integer) does not exist",Hint:fileListSignatureHint,Position:"8"},true,"prepare")
			}
			for _,tc := range []struct{args,token,code,message string}{
				{"NULL,'bad'","'bad'","22P02",`invalid input syntax for type boolean: "bad"`},
				{"NULL,'bad',1","'bad'","22P02",`invalid input syntax for type bigint: "bad"`},
				{"'x','bad',1,false","'bad'","22P02",`invalid input syntax for type bigint: "bad"`},
				{"NULL,'9223372036854775808',1","'9223372036854775808'","22003",`value "9223372036854775808" is out of range for type bigint`},
			} {
				q := "SELECT "+name+"("+tc.args+")"
				predefinedFailure(s,q,fixture.Diagnostic{Code:tc.code,Message:tc.message,Position:fmt.Sprint(strings.Index(q,tc.token)+1)},true,"prepare")
			}
		}
		// Named-notation grammar is checked independently of overload lookup.
		for _,tc := range []struct{call,token,message string}{
			{"pg_stat_file(filename => 'x', filename => 'y')","filename => 'y'",`argument name "filename" used more than once`},
			{"pg_ls_tmpdir(tablespace => NULL::oid, tablespace => NULL::oid)","tablespace => NULL::oid)",`argument name "tablespace" used more than once`},
			{"pg_stat_file(filename => 'x', false)","false","positional argument cannot follow named argument"},
			{"pg_ls_tmpdir(tablespace => NULL::oid, 0)","0)","positional argument cannot follow named argument"},
		} {
			q := "SELECT "+tc.call
			predefinedFailure(s,q,fixture.Diagnostic{Code:"42601",Message:tc.message,Position:fmt.Sprint(strings.Index(q,tc.token)+1)},true,"prepare")
		}
		for _,name := range []string{"pg_read_file","pg_read_binary_file","pg_stat_file","pg_ls_tmpdir"} {
			arg := "NULL::text"
			if name == "pg_ls_tmpdir" { arg = "NULL::oid" }
			for _,tc := range []struct{call,decoration,kind string}{
				{name+"(DISTINCT "+arg+")","DISTINCT","an aggregate function"},
				{name+"("+arg+" ORDER BY 1)","ORDER BY","an aggregate function"},
				{name+"("+arg+") FILTER (WHERE true)","FILTER","an aggregate function"},
				{name+"("+arg+") OVER ()","OVER","a window function nor an aggregate function"},
			} {
				predefinedFailure(s,"SELECT "+tc.call,fixture.Diagnostic{Code:"42809",Message:tc.decoration+" specified, but "+name+" is not "+tc.kind,Position:"8"},true,"prepare")
			}
		}
		// A valid NULL call must not execute a prefix and ignore invalid input.
		for _,tc := range []struct{q,token,message string}{
			{"SELECT pg_read_file(NULL::text) AS v garbage","garbage",`syntax error at or near "garbage"`},
			{"SELECT pg_ls_tmpdir(NULL::oid) AS v )",")",`syntax error at or near ")"`},
			{"SELECT pg_stat_file(","",`syntax error at end of input`},
		} {
			position := len(tc.q)+1
			if tc.token != "" { position = strings.LastIndex(tc.q,tc.token)+1 }
			predefinedFailure(s,tc.q,fixture.Diagnostic{Code:"42601",Message:tc.message,Position:fmt.Sprint(position)},true,"prepare")
		}
	})
}

func TestReferenceGenericFileReadPhysicalBoundary(t *testing.T) {
	fileReadCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		for _, sig := range fileReadSignatures[:8] {
			want := "18\n"
			if len(sig.args) >= 3 { want = "1" }
			fileReadVersion(s, sig.name, strings.Join(sig.args, ","), want)
		}
		for _, name := range []string{"pg_read_file", "pg_read_binary_file"} {
			for _, tc := range []struct{offset, text string}{{"1", "8"}, {"-1", "\n"}} {
				fileReadVersion(s, name, "'PG_VERSION',"+tc.offset+",1", tc.text)
			}
			for _,offset := range []string{"-9223372036854775808","9223372036854775807"} {
				call := name+"('gfr_missing_497356',"+fileListLiteral(offset)+"::bigint,0,true)"
				if s.Backend.PG { fileReadNull(s,call,name,false) } else { fileReadUnsupported(s,"SELECT "+call,name) }
			}
		}
		for _, name := range []string{"pg_read_file", "pg_read_binary_file", "pg_stat_file"} {
			for _, missing := range []bool{false,true} {
				q := fmt.Sprintf("SELECT * FROM %s('gfr_missing_497356',%t)", name, missing)
				if !s.Backend.PG { fileReadUnsupported(s, q, name) } else if missing {
					fileReadNull(s, fmt.Sprintf("%s('gfr_missing_497356',true)", name), name, true)
				} else {
					message := `could not open file "gfr_missing_497356" for reading: No such file or directory`
					if name == "pg_stat_file" { message = `could not stat file "gfr_missing_497356": No such file or directory` }
					predefinedFailure(s, q, fixture.Diagnostic{Code:"58P01", Message:message}, true, "execute")
				}
			}
		}
		q := "SELECT * FROM pg_stat_file('PG_VERSION')"
		if !s.Backend.PG { fileReadUnsupported(s,q,"pg_stat_file") } else {
			var size int64; var access,modification,change,creation any; var isdir bool
			if err := s.C.QueryRowContext(s.P.Ctx,q).Scan(&size,&access,&modification,&change,&creation,&isdir); err != nil || size != 3 || isdir || access == nil || modification == nil || change == nil || creation != nil { s.T.Fatal(size,access,modification,change,creation,isdir,err) }
		}
		for _, args := range []string{"", "1663::oid", "1664::oid"} {
			q := "SELECT * FROM pg_ls_tmpdir("+args+")"
			if !s.Backend.PG { fileReadUnsupported(s,q,"pg_ls_tmpdir"); continue }
			rows, err := s.C.QueryContext(s.P.Ctx,q)
			if err != nil { s.T.Fatal(err) }
			for rows.Next() {
				var name string; var size int64; var modified any
				if err := rows.Scan(&name,&size,&modified); err != nil { rows.Close(); s.T.Fatal(err) }
				if name == "" || strings.HasPrefix(name,".") || size < 0 || modified == nil { rows.Close(); s.T.Fatal(name,size,modified) }
			}
			if err := rows.Err(); err != nil { rows.Close(); s.T.Fatal(err) }; rows.Close()
		}
	})
}
